# ShubeR on Cloudflare Containers + R2

This version is prepared for Cloudflare Workers + Containers + R2.

## Architecture

- Cloudflare Worker is the public entrypoint.
- `ShubeRContainer` runs the Go application in a Cloudflare Container.
- R2 stores `data/shuber.json`, MP3/audio files and cover images.
- The Worker exposes `/media/*` directly from R2 for efficient streaming.
- The Go container accesses R2 only through the private virtual hostname `http://r2.internal` and an `outboundByHost` handler. R2 credentials are not placed inside the container.
- The container is configured with `max_instances: 1` because the current metadata/session store is a single JSON document. This keeps writes serialized. For multi-instance scale, migrate users/sessions to D1 or a dedicated database.

Cloudflare's current Containers model requires a Worker plus a Durable Object binding for the Container, and `wrangler deploy` can build and push a Dockerfile image. Containers are available on the Workers Paid plan. See the official docs linked below.

## 1. Create the R2 bucket

From the project directory:

```bash
npx wrangler login
npx wrangler r2 bucket create shuber-media
```

Or create the bucket in Cloudflare Dashboard > R2. If you choose another bucket name, change `bucket_name` in `wrangler.jsonc`.

## 2. Install dependencies

```bash
npm install
```

Wrangler 4 is used. The current package versions in this archive were checked against the published packages on September 30, 2026.

## 3. Set the admin password as a Worker secret

Do not put the real password into `wrangler.jsonc`.

```bash
npx wrangler secret put SHUBER_ADMIN_PASSWORD
```

Enter a strong unique password when prompted.

The default admin login configured in this archive is `admin`. Change `SHUBER_ADMIN_LOGIN` in `wrangler.jsonc` before deployment if desired.

## 4. Deploy

Docker must be running locally because the Wrangler config points `containers.image` at `Dockerfile.cloudflare`; Wrangler builds and pushes that image during deploy.

```bash
npm run deploy
```

After the first deployment, container provisioning can take several minutes. Check status with:

```bash
npx wrangler containers list
npx wrangler containers images list
```

## 5. Open the site

The deploy command gives you a `workers.dev` URL.

The project is configured with `workers_dev: true` so you can test immediately.

## 6. Attach your real domain

In Cloudflare Dashboard:

1. Workers & Pages
2. Open the `shuber-site` Worker
3. Settings > Domains & Routes
4. Add > Custom Domain
5. Enter your domain/subdomain

Custom Domains are the recommended setup when the Worker is the application's origin. Cloudflare provisions the DNS record and TLS certificate for the Worker.

## 7. Use the music CMS

Open the public site and sign in with the admin account.

The account button opens the music CMS for `admin`.

Add:

- title
- subtitle
- BPM
- mood
- audio file
- cover image

Uploaded files are written to R2, and the public `/media/...` URLs are served by the Worker directly from R2. The archive defaults to a 90 MiB application upload limit; Cloudflare account-level request-body limits still apply, so larger audio files may require a higher Cloudflare plan/limit.

## 8. Backups

The R2 bucket contains the important site data:

```text
 data/shuber.json
 media/audio/...
 media/covers/...
```

You can manage and export these objects from the R2 Dashboard or with the R2/S3-compatible API.

## 9. Local development

For local app-only development without Cloudflare Containers:

```bash
go run ./cmd/server
```

To run the Cloudflare Worker + Container locally with Wrangler:

```bash
npm run dev
```

Docker must be available for local Container development.

## Important production note

The current user/session metadata store intentionally remains a JSON document so this Cloudflare version stays close to the starter you already have. It is suitable for a personal single-instance music site. It is not the right design for many concurrent writers or multiple application instances.

Before adding large-scale accounts, comments, likes, analytics, or collaborative admin editing, move users/sessions/metadata to D1 (or another database) while keeping R2 for media.

## Official docs

- Containers: https://developers.cloudflare.com/containers/get-started/
- Container configuration: https://developers.cloudflare.com/containers/reference/wrangler-configuration/
- Container outbound handlers: https://developers.cloudflare.com/containers/guides/outbound-traffic/
- R2 Workers API: https://developers.cloudflare.com/r2/api/workers/workers-api-reference/
- Custom Domains: https://developers.cloudflare.com/workers/configuration/routing/custom-domains/
- Containers pricing: https://developers.cloudflare.com/containers/platform/pricing/
