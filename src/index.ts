interface R2Binding {
  get(key: string, options?: any): Promise<any>;
  head(key: string): Promise<any>;
  put(key: string, value: any, options?: any): Promise<any>;
  delete(key: string): Promise<void>;
}

interface Env {
  ASSETS: { fetch(request: Request): Promise<Response> };
  MEDIA_BUCKET: R2Binding;
  DATA_KEY?: string;
  COOKIE_SECURE?: string;
  SESSION_TTL_SECONDS?: string;
  MAX_UPLOAD_BYTES?: string;
  SHUBER_ADMIN_LOGIN?: string;
  SHUBER_ADMIN_PASSWORD: string;
  SESSION_SECRET: string;
  APP_ENV?: string;
}

interface UserRecord {
  id: number;
  login: string;
  passwordHash: string;
  role: "user" | "admin";
  createdAt: string;
}

interface Track {
  id: string;
  title: string;
  subtitle?: string;
  bpm?: number;
  mood?: string;
  audio_path?: string;
  cover_path?: string;
  createdAt: string;
}

interface SiteData {
  nextUserId: number;
  users: Record<string, UserRecord>;
  tracks: Record<string, Track>;
}

interface Identity {
  id: number;
  login: string;
  role: "user" | "admin";
}

const encoder = new TextEncoder();
const decoder = new TextDecoder();

const DEFAULT_DATA_KEY = "data/shuber.json";
const DEFAULT_TTL = 7 * 24 * 60 * 60;
const DEFAULT_MAX_UPLOAD = 90 * 1024 * 1024;
const AUDIO_EXT = new Set([".mp3", ".ogg", ".wav", ".m4a", ".aac", ".flac"]);
const COVER_EXT = new Set([".webp", ".jpg", ".jpeg", ".png"]);

const attempts = new Map<string, { start: number; count: number }>();

function cleanLogin(value: string): string {
  return value.trim().toLowerCase();
}

function json(data: unknown, init: ResponseInit = {}): Response {
  const headers = new Headers(init.headers);
  headers.set("content-type", "application/json; charset=utf-8");
  headers.set("cache-control", "no-store");
  return new Response(JSON.stringify(data), { ...init, headers });
}

function error(message: string, status = 400): Response {
  return json({ error: message }, { status });
}

function requestOriginAllowed(request: Request): boolean {
  const origin = request.headers.get("Origin");
  if (!origin) return true;
  return origin === new URL(request.url).origin;
}

function setSecurityHeaders(response: Response): Response {
  const headers = new Headers(response.headers);
  headers.set("X-Content-Type-Options", "nosniff");
  headers.set("Referrer-Policy", "strict-origin-when-cross-origin");
  headers.set("X-Frame-Options", "DENY");
  headers.set("Permissions-Policy", "camera=(), microphone=(), geolocation=()");
  headers.set(
    "Content-Security-Policy",
    "default-src 'self'; img-src 'self' data:; media-src 'self' blob:; font-src 'self' https://fonts.gstatic.com; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; script-src 'self'; connect-src 'self'; frame-ancestors 'none'"
  );
  if (new URL(response.url || "https://shuber.invalid").protocol === "https:" || headers.get("Strict-Transport-Security")) {
    headers.set("Strict-Transport-Security", "max-age=31536000; includeSubDomains");
  }
  return new Response(response.body, { status: response.status, statusText: response.statusText, headers });
}

function cookieOptions(env: Env): string {
  const secure = env.COOKIE_SECURE !== "false" ? "; Secure" : "";
  const maxAge = Number(env.SESSION_TTL_SECONDS || DEFAULT_TTL);
  return `Path=/; HttpOnly; SameSite=Lax${secure}; Max-Age=${Number.isFinite(maxAge) ? Math.max(0, Math.floor(maxAge)) : DEFAULT_TTL}`;
}

function clearCookieOptions(env: Env): string {
  const secure = env.COOKIE_SECURE !== "false" ? "; Secure" : "";
  return `Path=/; HttpOnly; SameSite=Lax${secure}; Max-Age=0`;
}

function getCookie(request: Request, name: string): string | null {
  const raw = request.headers.get("Cookie");
  if (!raw) return null;
  for (const part of raw.split(";")) {
    const index = part.indexOf("=");
    if (index < 0) continue;
    if (part.slice(0, index).trim() === name) return decodeURIComponent(part.slice(index + 1).trim());
  }
  return null;
}

function toBase64Url(bytes: Uint8Array): string {
  let binary = "";
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, Math.min(i + chunk, bytes.length)));
  }
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

function fromBase64Url(value: string): Uint8Array {
  const padded = value.replace(/-/g, "+").replace(/_/g, "/") + "===".slice((value.length + 3) % 4);
  const binary = atob(padded);
  const out = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i);
  return out;
}

async function sha256(value: string): Promise<Uint8Array> {
  return new Uint8Array(await crypto.subtle.digest("SHA-256", encoder.encode(value)));
}

async function hmacSha256(secret: string, value: string): Promise<Uint8Array> {
  const key = await crypto.subtle.importKey(
    "raw",
    encoder.encode(secret),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"]
  );
  return new Uint8Array(await crypto.subtle.sign("HMAC", key, encoder.encode(value)));
}

function timingEqual(a: Uint8Array, b: Uint8Array): boolean {
  const subtle = crypto.subtle as SubtleCrypto & {
    timingSafeEqual(a: ArrayBufferView, b: ArrayBufferView): boolean;
  };
  if (a.length !== b.length) {
    return !subtle.timingSafeEqual(a, a);
  }
  return subtle.timingSafeEqual(a, b);
}

async function passwordHash(password: string): Promise<string> {
  const salt = new Uint8Array(16);
  crypto.getRandomValues(salt);
  const baseKey = await crypto.subtle.importKey("raw", encoder.encode(password), "PBKDF2", false, ["deriveBits"]);
  const bits = new Uint8Array(
    await crypto.subtle.deriveBits(
      { name: "PBKDF2", salt, iterations: 100_000, hash: "SHA-256" },
      baseKey,
      256
    )
  );
  return `pbkdf2$100000$${toBase64Url(salt)}$${toBase64Url(bits)}`;
}

async function passwordVerify(encoded: string, password: string): Promise<boolean> {
  const parts = encoded.split("$");
  if (parts.length !== 4 || parts[0] !== "pbkdf2") return false;
  const iterations = Number(parts[1]);
  if (!Number.isInteger(iterations) || iterations < 50_000 || iterations > 300_000) return false;
  let salt: Uint8Array;
  let expected: Uint8Array;
  try {
    salt = fromBase64Url(parts[2]);
    expected = fromBase64Url(parts[3]);
  } catch {
    return false;
  }
  const baseKey = await crypto.subtle.importKey("raw", encoder.encode(password), "PBKDF2", false, ["deriveBits"]);
  const actual = new Uint8Array(
    await crypto.subtle.deriveBits(
      { name: "PBKDF2", salt, iterations, hash: "SHA-256" },
      baseKey,
      expected.length * 8
    )
  );
  return timingEqual(actual, expected);
}

function getDataKey(env: Env): string {
  return env.DATA_KEY || DEFAULT_DATA_KEY;
}

function emptyData(): SiteData {
  return { nextUserId: 1, users: {}, tracks: {} };
}

async function loadData(env: Env): Promise<SiteData> {
  const object = await env.MEDIA_BUCKET.get(getDataKey(env));
  if (!object) return emptyData();
  try {
    const parsed = JSON.parse(await object.text()) as Partial<SiteData>;
    return {
      nextUserId: parsed.nextUserId && parsed.nextUserId > 0 ? parsed.nextUserId : 1,
      users: parsed.users || {},
      tracks: parsed.tracks || {},
    };
  } catch {
    throw new Error("stored site data is invalid");
  }
}

async function saveData(env: Env, data: SiteData): Promise<void> {
  await env.MEDIA_BUCKET.put(getDataKey(env), JSON.stringify(data, null, 2), {
    httpMetadata: { contentType: "application/json; charset=utf-8", cacheControl: "no-store" },
  });
}

async function makeSession(identity: Identity, env: Env): Promise<string> {
  const ttl = Math.max(60, Number(env.SESSION_TTL_SECONDS || DEFAULT_TTL));
  const payload = toBase64Url(encoder.encode(JSON.stringify({
    sub: identity.id,
    login: identity.login,
    role: identity.role,
    exp: Math.floor(Date.now() / 1000) + ttl,
  })));
  const signature = toBase64Url(await hmacSha256(env.SESSION_SECRET, payload));
  return `${payload}.${signature}`;
}

async function readSession(request: Request, env: Env): Promise<Identity | null> {
  const token = getCookie(request, "shuber_session");
  if (!token) return null;
  const dot = token.lastIndexOf(".");
  if (dot <= 0) return null;
  const payloadPart = token.slice(0, dot);
  const signaturePart = token.slice(dot + 1);
  let payloadBytes: Uint8Array;
  let signature: Uint8Array;
  try {
    payloadBytes = fromBase64Url(payloadPart);
    signature = fromBase64Url(signaturePart);
  } catch {
    return null;
  }
  const expected = await hmacSha256(env.SESSION_SECRET, payloadPart);
  if (!timingEqual(expected, signature)) return null;
  try {
    const payload = JSON.parse(decoder.decode(payloadBytes)) as { sub: number; login: string; role: "user" | "admin"; exp: number };
    if (!payload.exp || payload.exp < Math.floor(Date.now() / 1000)) return null;
    if (!payload.login || (payload.role !== "user" && payload.role !== "admin")) return null;
    return { id: payload.sub, login: payload.login, role: payload.role };
  } catch {
    return null;
  }
}

function allowAttempt(key: string, max: number, windowMs: number): boolean {
  const now = Date.now();
  const current = attempts.get(key);
  if (!current || now - current.start >= windowMs) {
    attempts.set(key, { start: now, count: 1 });
    return true;
  }
  if (current.count >= max) return false;
  current.count++;
  attempts.set(key, current);
  return true;
}

function getIp(request: Request): string {
  return request.headers.get("CF-Connecting-IP") || "unknown";
}

function extFromFilename(name: string): string {
  const dot = name.lastIndexOf(".");
  return dot >= 0 ? name.slice(dot).toLowerCase() : "";
}

function safeTrackId(id: string): string {
  const normalized = id.trim();
  if (!/^[a-zA-Z0-9_-]{1,40}$/.test(normalized)) throw new Error("invalid track id");
  return normalized;
}

function contentTypeForExt(ext: string): string {
  switch (ext) {
    case ".mp3": return "audio/mpeg";
    case ".ogg": return "audio/ogg";
    case ".wav": return "audio/wav";
    case ".m4a": return "audio/mp4";
    case ".aac": return "audio/aac";
    case ".flac": return "audio/flac";
    case ".webp": return "image/webp";
    case ".jpg":
    case ".jpeg": return "image/jpeg";
    case ".png": return "image/png";
    default: return "application/octet-stream";
  }
}

function adminRequired(identity: Identity | null): Response | null {
  if (!identity) return error("unauthorized", 401);
  if (identity.role !== "admin") return error("forbidden", 403);
  return null;
}

function sortTracks(tracks: Record<string, Track>): Track[] {
  return Object.values(tracks).sort((a, b) => b.createdAt.localeCompare(a.createdAt));
}

function parseRange(value: string | null, size: number): { offset: number; length: number; start: number; end: number } | null {
  if (!value || !/^bytes=\d*-\d*$/.test(value.trim()) || size <= 0) return null;
  const [startRaw, endRaw] = value.trim().slice(6).split("-");
  let start: number;
  let end: number;
  if (!startRaw) {
    const suffix = Number(endRaw);
    if (!Number.isInteger(suffix) || suffix <= 0) return null;
    const length = Math.min(suffix, size);
    start = size - length;
    end = size - 1;
  } else {
    start = Number(startRaw);
    if (!Number.isInteger(start) || start < 0 || start >= size) return null;
    end = endRaw ? Number(endRaw) : size - 1;
    if (!Number.isInteger(end) || end < start) return null;
    end = Math.min(end, size - 1);
  }
  return { offset: start, length: end - start + 1, start, end };
}

async function serveMedia(request: Request, env: Env): Promise<Response> {
  const pathname = new URL(request.url).pathname;
  const key = pathname.slice("/media/".length);
  if (!key || key.includes("..") || key.startsWith("/")) return error("bad media path", 400);
  if (request.method !== "GET" && request.method !== "HEAD") return error("method not allowed", 405);

  const head = await env.MEDIA_BUCKET.head("media/" + key);
  if (!head) return error("not found", 404);

  const baseHeaders = new Headers();
  head.writeHttpMetadata(baseHeaders);
  baseHeaders.set("ETag", head.httpEtag);
  baseHeaders.set("Accept-Ranges", "bytes");
  baseHeaders.set("Cache-Control", "public, max-age=31536000, immutable");
  baseHeaders.set("Content-Length", String(head.size));

  if (request.method === "HEAD") return new Response(null, { status: 200, headers: baseHeaders });

  const range = parseRange(request.headers.get("Range"), head.size);
  if (request.headers.has("Range") && !range) {
    return new Response(null, {
      status: 416,
      headers: new Headers({ "Content-Range": `bytes */${head.size}` }),
    });
  }

  const object = range
    ? await env.MEDIA_BUCKET.get("media/" + key, { range: { offset: range.offset, length: range.length } })
    : await env.MEDIA_BUCKET.get("media/" + key);

  if (!object || !("body" in object)) return error("not found", 404);

  if (range) {
    baseHeaders.set("Content-Range", `bytes ${range.start}-${range.end}/${head.size}`);
    baseHeaders.set("Content-Length", String(range.length));
    return new Response(object.body, { status: 206, headers: baseHeaders });
  }
  return new Response(object.body, { status: 200, headers: baseHeaders });
}

async function handleApi(request: Request, env: Env): Promise<Response> {
  const url = new URL(request.url);
  const path = url.pathname;
  const method = request.method;

  if (method === "OPTIONS") return new Response(null, { status: 204 });

  if (path === "/api/health" && method === "GET") return json({ ok: true, env: env.APP_ENV || "production" });

  if ((method === "POST" || method === "DELETE") && !requestOriginAllowed(request)) {
    return error("forbidden origin", 403);
  }

  if (path === "/api/tracks" && method === "GET") {
    const data = await loadData(env);
    return json({ tracks: sortTracks(data.tracks) });
  }

  if (path === "/api/me" && method === "GET") {
    const identity = await readSession(request, env);
    if (!identity) return error("unauthorized", 401);
    return json(identity);
  }

  if (path === "/api/register" && method === "POST") {
    if (!allowAttempt(`register:${getIp(request)}`, 8, 10 * 60 * 1000)) return error("too many attempts", 429);
    const body = await request.json().catch(() => null) as { login?: string; password?: string } | null;
    const login = cleanLogin(body?.login || "");
    const password = body?.password || "";
    const adminLogin = cleanLogin(env.SHUBER_ADMIN_LOGIN || "admin");
    if (login === adminLogin) return error("reserved login", 400);
    if (login.length < 3 || login.length > 64 || password.length < 6 || password.length > 200) {
      return error("login 3-64 chars, password 6-200 chars", 400);
    }
    const data = await loadData(env);
    if (data.users[login]) return error("login already exists", 409);
    const id = data.nextUserId++;
    data.users[login] = {
      id,
      login,
      passwordHash: await passwordHash(password),
      role: "user",
      createdAt: new Date().toISOString(),
    };
    await saveData(env, data);
    return json({ id, login, role: "user" }, { status: 201 });
  }

  if (path === "/api/login" && method === "POST") {
    if (!allowAttempt(`login:${getIp(request)}`, 10, 10 * 60 * 1000)) return error("too many attempts", 429);
    const body = await request.json().catch(() => null) as { login?: string; password?: string } | null;
    const login = cleanLogin(body?.login || "");
    const password = body?.password || "";
    let identity: Identity | null = null;

    const adminLogin = cleanLogin(env.SHUBER_ADMIN_LOGIN || "admin");
    if (login === adminLogin) {
      if (!env.SHUBER_ADMIN_PASSWORD || !(timingEqual(await sha256(password), await sha256(env.SHUBER_ADMIN_PASSWORD)))) {
        return error("invalid credentials", 401);
      }
      identity = { id: 0, login: adminLogin, role: "admin" };
    } else {
      const data = await loadData(env);
      const user = data.users[login];
      if (!user || !(await passwordVerify(user.passwordHash, password))) return error("invalid credentials", 401);
      identity = { id: user.id, login: user.login, role: user.role };
    }

    const token = await makeSession(identity, env);
    return json(identity, {
      headers: { "Set-Cookie": `shuber_session=${encodeURIComponent(token)}; ${cookieOptions(env)}` },
    });
  }

  if (path === "/api/logout" && method === "POST") {
    return json({ ok: true }, {
      headers: { "Set-Cookie": `shuber_session=; ${clearCookieOptions(env)}` },
    });
  }

  if (path === "/api/admin/tracks" && method === "POST") {
    const identity = await readSession(request, env);
    const denied = adminRequired(identity);
    if (denied) return denied;

    const body = await request.json().catch(() => null) as Partial<Track> | null;
    const title = (body?.title || "").trim();
    if (!title || title.length > 160) return error("title required (max 160 chars)", 400);

    const data = await loadData(env);
    let id = (body?.id || "").trim();
    if (!id) id = String(Math.max(1, Object.keys(data.tracks).length + 1)).padStart(2, "0");
    try { id = safeTrackId(id); } catch (e) { return error(e instanceof Error ? e.message : "invalid track id", 400); }

    const previous = data.tracks[id];
    const track: Track = {
      id,
      title,
      subtitle: String(body?.subtitle || "").trim(),
      mood: String(body?.mood || "").trim(),
      bpm: Number.isFinite(Number(body?.bpm)) && Number(body?.bpm) > 0 ? Math.round(Number(body?.bpm)) : undefined,
      audio_path: previous?.audio_path,
      cover_path: previous?.cover_path,
      createdAt: previous?.createdAt || new Date().toISOString(),
    };
    data.tracks[id] = track;
    await saveData(env, data);
    return json(track, { status: previous ? 200 : 201 });
  }

  const uploadMatch = path.match(/^\/api\/admin\/upload\/(audio|cover)\/([^/]+)$/);
  if (uploadMatch && method === "POST") {
    const identity = await readSession(request, env);
    const denied = adminRequired(identity);
    if (denied) return denied;

    const type = uploadMatch[1] as "audio" | "cover";
    let id = "";
    try { id = safeTrackId(decodeURIComponent(uploadMatch[2])); } catch { return error("invalid track id", 400); }

    const data = await loadData(env);
    const track = data.tracks[id];
    if (!track) return error("track not found", 404);
    const filename = url.searchParams.get("filename") || request.headers.get("X-Filename") || "";
    const ext = extFromFilename(filename);
    const allowed = type === "audio" ? AUDIO_EXT : COVER_EXT;
    if (!allowed.has(ext)) return error("unsupported file type", 400);

    const sizeHeader = request.headers.get("Content-Length");
    const size = sizeHeader ? Number(sizeHeader) : 0;
    const maxBytes = Math.max(1, Number(env.MAX_UPLOAD_BYTES || DEFAULT_MAX_UPLOAD));
    const effectiveMax = type === "cover" ? Math.min(maxBytes, 10 * 1024 * 1024) : maxBytes;
    if (size && size > effectiveMax) {
      return error(`file too large (max ${Math.floor(effectiveMax / 1024 / 1024)} MB)`, 413);
    }
    if (!request.body) return error("file body required", 400);

    const prefix = type === "audio" ? "audio" : "covers";
    const previousPath = type === "audio" ? track.audio_path : track.cover_path;
    if (previousPath) {
      const previousKey = String(previousPath).replace(/^\//, "").split(/[?#]/, 1)[0];
      if (previousKey) await env.MEDIA_BUCKET.delete(previousKey);
    }
    const version = Date.now();
    const key = `media/${prefix}/track-${id}-${version}${ext}`;
    await env.MEDIA_BUCKET.put(key, request.body, {
      httpMetadata: {
        contentType: contentTypeForExt(ext),
        cacheControl: "public, max-age=31536000, immutable",
      },
    });

    if (type === "audio") {
      track.audio_path = "/" + key;
    } else {
      track.cover_path = "/" + key;
    }
    data.tracks[id] = track;
    await saveData(env, data);
    return json({ ok: true, path: "/" + key, track });
  }

  const trackUpdateMatch = path.match(/^\/api\/admin\/tracks\/([^/]+)$/);
  if (trackUpdateMatch && (method === "PUT" || method === "PATCH")) {
    const identity = await readSession(request, env);
    const denied = adminRequired(identity);
    if (denied) return denied;
    let id = "";
    try { id = safeTrackId(decodeURIComponent(trackUpdateMatch[1])); } catch { return error("invalid track id", 400); }

    const data = await loadData(env);
    const track = data.tracks[id];
    if (!track) return error("track not found", 404);
    const body = await request.json().catch(() => null) as Partial<Track> | null;
    if (body?.title !== undefined) track.title = String(body.title).trim();
    if (body?.subtitle !== undefined) track.subtitle = String(body.subtitle).trim();
    if (body?.mood !== undefined) track.mood = String(body.mood).trim();
    if (body?.bpm !== undefined) track.bpm = Number.isFinite(Number(body.bpm)) ? Math.round(Number(body.bpm)) : undefined;
    await saveData(env, data);
    return json(track);
  }

  if (trackUpdateMatch && method === "DELETE") {
    const identity = await readSession(request, env);
    const denied = adminRequired(identity);
    if (denied) return denied;
    let id = "";
    try { id = safeTrackId(decodeURIComponent(trackUpdateMatch[1])); } catch { return error("invalid track id", 400); }

    const data = await loadData(env);
    const track = data.tracks[id];
    if (!track) return error("track not found", 404);

    const keys = [track.audio_path, track.cover_path]
      .filter(Boolean)
      .map((v) => String(v).replace(/^\//, ""));
    for (const key of keys) {
      await env.MEDIA_BUCKET.delete(key);
    }
    delete data.tracks[id];
    await saveData(env, data);
    return json({ ok: true });
  }

  return error("not found", 404);
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    try {
      const url = new URL(request.url);

      if (url.pathname.startsWith("/media/")) {
        return setSecurityHeaders(await serveMedia(request, env));
      }

      if (url.pathname.startsWith("/api/")) {
        return setSecurityHeaders(await handleApi(request, env));
      }

      const response = await env.ASSETS.fetch(request);
      return setSecurityHeaders(response);
    } catch (err) {
      console.error(err);
      return setSecurityHeaders(error("internal server error", 500));
    }
  },
};
