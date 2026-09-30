import { Container, ContainerProxy, getContainer } from "@cloudflare/containers";

interface Env {
  SHUBER_CONTAINER: DurableObjectNamespace<ShubeRContainer>;
  MEDIA_BUCKET: R2Bucket;
  R2_INTERNAL_URL: string;
}

const R2_HOST = "r2.internal";
const MEDIA_PREFIX = "media/";
const DATA_PREFIX = "data/";

function keyFromRequest(request: Request): string | null {
  const url = new URL(request.url);
  let key = url.pathname.replace(/^\/+/, "");
  try {
    key = decodeURIComponent(key);
  } catch {
    return null;
  }
  if (!key || key.includes("..") || key.startsWith("/") || (!key.startsWith(MEDIA_PREFIX) && !key.startsWith(DATA_PREFIX))) {
    return null;
  }
  return key;
}

function parseSingleRange(header: string | null, size: number): { offset: number; length: number; start: number; end: number } | null {
  if (!header || size <= 0) return null;
  const match = /^bytes=(\d*)-(\d*)$/.exec(header.trim());
  if (!match) return null;
  const [, startRaw, endRaw] = match;
  let start: number;
  let end: number;
  if (startRaw === "") {
    const suffix = Number(endRaw);
    if (!Number.isInteger(suffix) || suffix <= 0) return null;
    const length = Math.min(suffix, size);
    start = size - length;
    end = size - 1;
  } else {
    start = Number(startRaw);
    if (!Number.isInteger(start) || start < 0 || start >= size) return null;
    end = endRaw === "" ? size - 1 : Number(endRaw);
    if (!Number.isInteger(end) || end < start) return null;
    end = Math.min(end, size - 1);
  }
  return { offset: start, length: end - start + 1, start, end };
}

async function serveMedia(request: Request, env: Env): Promise<Response> {
  const url = new URL(request.url);
  const key = url.pathname.slice("/media/".length);
  let decodedKey: string;
  try {
    decodedKey = decodeURIComponent(key);
  } catch {
    return new Response("Bad path", { status: 400 });
  }
  if (!decodedKey || decodedKey.includes("..") || decodedKey.startsWith("/")) {
    return new Response("Bad path", { status: 400 });
  }

  const range = parseSingleRange(request.headers.get("Range"), Number.MAX_SAFE_INTEGER);
  let object;
  if (request.method === "HEAD") {
    object = await env.MEDIA_BUCKET.head(MEDIA_PREFIX + decodedKey);
    if (!object) return new Response("Not Found", { status: 404 });
    const headers = new Headers();
    object.writeHttpMetadata(headers);
    headers.set("ETag", object.httpEtag);
    headers.set("Accept-Ranges", "bytes");
    headers.set("Cache-Control", headers.get("Cache-Control") || "public, max-age=3600");
    headers.set("Content-Length", String(object.size));
    return new Response(null, { status: 200, headers });
  }
  if (request.method !== "GET") {
    return new Response("Method Not Allowed", { status: 405, headers: { Allow: "GET, HEAD" } });
  }

  // Read object metadata first when a Range header is present so we can form a correct R2 range.
  const head = request.headers.has("Range") ? await env.MEDIA_BUCKET.head(MEDIA_PREFIX + decodedKey) : null;
  if (request.headers.has("Range") && !head) return new Response("Not Found", { status: 404 });
  const parsed = head ? parseSingleRange(request.headers.get("Range"), head.size) : null;
  if (request.headers.has("Range") && !parsed) {
    return new Response("Range Not Satisfiable", { status: 416, headers: { "Content-Range": `bytes */${head?.size ?? 0}` } });
  }

  const getOptions: R2GetOptions = {};
  if (request.headers.has("If-None-Match") || request.headers.has("If-Modified-Since")) {
    getOptions.onlyIf = request.headers;
  }
  if (parsed) getOptions.range = { offset: parsed.offset, length: parsed.length };
  object = await env.MEDIA_BUCKET.get(MEDIA_PREFIX + decodedKey, getOptions);
  if (!object) return new Response("Not Found", { status: 404 });
  if (!("body" in object)) return new Response(null, { status: 412 });

  const headers = new Headers();
  object.writeHttpMetadata(headers);
  headers.set("ETag", object.httpEtag);
  headers.set("Accept-Ranges", "bytes");
  headers.set("Cache-Control", headers.get("Cache-Control") || "public, max-age=3600");
  if (parsed && head) {
    headers.set("Content-Range", `bytes ${parsed.start}-${parsed.end}/${head.size}`);
    headers.set("Content-Length", String(parsed.length));
    return new Response(object.body, { status: 206, headers });
  }
  headers.set("Content-Length", String(object.size));
  return new Response(object.body, { status: 200, headers });
}

async function handleR2Internal(request: Request, env: Env): Promise<Response> {
  const key = keyFromRequest(request);
  if (!key) return new Response("Forbidden", { status: 403 });

  switch (request.method) {
    case "GET": {
      const object = await env.MEDIA_BUCKET.get(key, { onlyIf: request.headers, range: request.headers });
      if (!object) return new Response("Object Not Found", { status: 404 });
      if (!("body" in object)) return new Response(null, { status: 412 });
      const headers = new Headers();
      object.writeHttpMetadata(headers);
      headers.set("ETag", object.httpEtag);
      return new Response(object.body, { status: 200, headers });
    }
    case "HEAD": {
      const object = await env.MEDIA_BUCKET.head(key);
      if (!object) return new Response("Object Not Found", { status: 404 });
      const headers = new Headers();
      object.writeHttpMetadata(headers);
      headers.set("ETag", object.httpEtag);
      headers.set("Content-Length", String(object.size));
      return new Response(null, { status: 200, headers });
    }
    case "PUT": {
      if (!request.body) return new Response("Request body required", { status: 400 });
      const saved = await env.MEDIA_BUCKET.put(key, request.body, {
        httpMetadata: request.headers,
      });
      return new Response(JSON.stringify({ ok: true, key, etag: saved?.httpEtag ?? null }), {
        headers: { "content-type": "application/json; charset=utf-8" },
      });
    }
    case "DELETE":
      await env.MEDIA_BUCKET.delete(key);
      return new Response(JSON.stringify({ ok: true }), { headers: { "content-type": "application/json" } });
    default:
      return new Response("Method Not Allowed", { status: 405, headers: { Allow: "GET, HEAD, PUT, DELETE" } });
  }
}

export class ShubeRContainer extends Container<Env> {
  defaultPort = 8080;
  sleepAfter = "20m";
  enableInternet = false;
  allowedHosts = [R2_HOST];

  envVars = {
    APP_ENV: this.env.APP_ENV,
    STORAGE_BACKEND: this.env.STORAGE_BACKEND,
    R2_INTERNAL_URL: this.env.R2_INTERNAL_URL,
    DATA_KEY: this.env.DATA_KEY,
    COOKIE_SECURE: this.env.COOKIE_SECURE,
    SESSION_TTL: this.env.SESSION_TTL,
    MAX_UPLOAD_BYTES: this.env.MAX_UPLOAD_BYTES,
    SHUBER_ADMIN_LOGIN: this.env.SHUBER_ADMIN_LOGIN,
    SHUBER_ADMIN_PASSWORD: this.env.SHUBER_ADMIN_PASSWORD,
  };

  static outboundByHost = {
    [R2_HOST]: async (request: Request, env: Env) => handleR2Internal(request, env),
  };
}

export { ContainerProxy };

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);
    if (url.pathname.startsWith("/media/")) {
      return serveMedia(request, env);
    }
    return getContainer(env.SHUBER_CONTAINER).fetch(request);
  },
};
