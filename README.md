# ShubeR Site · Cloudflare Workers Free

This version does **not** use Cloudflare Containers or Durable Objects.

## Architecture

- Cloudflare Workers Free: API + authentication
- Workers Assets: HTML/CSS/JS
- R2: music files, covers and small site data
- Signed HttpOnly session cookie

Cloudflare Workers Free currently includes 100,000 requests/day. Static assets are served without Workers request charges, while R2 Standard currently includes 10 GB-month, 1M Class A operations, 10M Class B operations, and free egress each month. See Cloudflare pricing/limits docs before production use.

## Cloudflare setup

### 1. R2

Create a Standard bucket:

`shuber-media`

Keep public access disabled.

### 2. Worker secrets

In the Worker dashboard add these encrypted secrets:

- `SHUBER_ADMIN_PASSWORD` — password for the admin login
- `SESSION_SECRET` — long random secret used to sign session cookies

Do not commit either secret to GitHub.

### 3. Build settings

- Build command: empty
- Deploy command: `npx wrangler deploy`
- Root directory: `/`

### 4. GitHub

Pushes to `main` can trigger the Cloudflare build configured for this repository.

## Admin

The default admin login is:

`admin`

The password comes only from `SHUBER_ADMIN_PASSWORD`.

After deployment:

1. Open the site.
2. Click **Войти**.
3. Login as `admin`.
4. The admin music CMS appears.
5. Add a track, then upload an audio file and a cover.

Audio is uploaded directly through the Worker to R2. The Worker streams audio back with HTTP Range support, so the HTML5 player can seek normally.

## Upload limits

The application is configured for up to 90 MiB audio uploads and 10 MiB cover uploads. Cloudflare's current Workers Free request body limit is 100 MB, so larger audio files should later be moved to a multipart/direct-upload flow.

## Important

This repository also contains older Go source files from the previous Container version. They are not used by the current `wrangler.jsonc` deployment. The active production entry point is `src/index.ts`.
