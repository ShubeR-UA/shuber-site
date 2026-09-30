# ShubeR Site · Cloudflare Containers + R2

Cloudflare-ready production starter for the ShubeR music site.

## Stack

- Go application inside a Cloudflare Container
- Cloudflare Worker as the public edge entrypoint
- Cloudflare R2 for site metadata, audio files and cover images
- Durable Object binding required by Cloudflare Containers
- server-side sessions with HttpOnly cookie
- role-based admin CMS
- direct R2 media delivery with byte-range support for the HTML5 player
- rate limiting for login/register
- same-origin protection for state-changing requests
- security headers + HSTS in production
- Workers observability enabled

## Architecture

```text
Browser
   |
   v
Cloudflare Worker
   |
   +---- /media/* ----> R2
   |
   +---- everything else ----> ShubeRContainer (Go)
                                      |
                                      +---- private http://r2.internal ----> R2
```

The Go container does not receive R2 API credentials. The Worker uses `outboundByHost` to expose a private virtual host to the container and performs the R2 operation through its R2 binding. Cloudflare documents this pattern for connecting Containers to R2 without an SDK inside the container.

The current JSON metadata/session store is kept in `data/shuber.json` inside R2 and the container is intentionally limited to one instance. That keeps the existing starter architecture simple and consistent. For a multi-instance public platform, migrate users/sessions/metadata to D1 or another transactional database while keeping R2 for media.

## What you need

- Cloudflare account
- Workers Paid plan because Containers are available on Workers Paid
- Node.js supported by the current Wrangler release
- Docker for `wrangler deploy` from your own machine when the Container image is built from `Dockerfile.cloudflare`
- an R2 bucket

## Fast deployment from your computer

### 1. Create R2

```bash
npx wrangler login
npx wrangler r2 bucket create shuber-media
```

If you create the bucket manually in Dashboard > R2, keep the name `shuber-media` or change `bucket_name` in `wrangler.jsonc`.

### 2. Install dependencies

```bash
npm install
```

### 3. Set the admin password as a secret

```bash
npx wrangler secret put SHUBER_ADMIN_PASSWORD
```

The default admin login is `admin`. Change `SHUBER_ADMIN_LOGIN` in `wrangler.jsonc` before deployment if needed.

### 4. Deploy

```bash
npm run deploy
```

The current Wrangler configuration points the Container image at `Dockerfile.cloudflare`, so a local Docker engine must be running when deploying from your machine.

After the first deploy, Cloudflare may need several minutes to provision the Container. You can check it with:

```bash
npx wrangler containers list
npx wrangler containers images list
```

### 5. Test the site

Open the `workers.dev` URL printed by Wrangler.

Log in as the admin and open the account button to use the music CMS.

## Deploy through Cloudflare Dashboard + GitHub

This is the better route if you want future deployments to happen automatically after a Git push.

1. Put this project in a GitHub repository.
2. In Cloudflare Dashboard open **Workers & Pages**.
3. Select **Create application** > **Get started** next to **Import a repository**.
4. Select the GitHub repository.
5. Use `npx wrangler deploy` as the production deploy command.
6. Keep `wrangler.jsonc` and `Dockerfile.cloudflare` at the repository root.
7. Add `SHUBER_ADMIN_PASSWORD` as a Worker secret in Cloudflare.
8. Deploy the production branch.

Cloudflare Workers Builds supports Workers that use Containers when the production deploy command is `npx wrangler deploy`; the build environment can build the Dockerfile image for you.

## Attach your domain

After the Worker exists:

**Workers & Pages > shuber-site > Settings > Domains & Routes > Add > Custom Domain**

Enter something like `music.example.com`.

Cloudflare will create the DNS record and certificate for the Worker Custom Domain.

The Wrangler file deliberately keeps `workers_dev` enabled so the site is immediately testable. You can manage the final custom domain in the dashboard.

## Using the CMS

The public site has the existing ShubeR player and account UI.

Admin flow:

```text
Account
  -> sign in as admin
  -> Admin / Music CMS
  -> title / subtitle / BPM / mood
  -> audio file
  -> cover image
  -> save
```

Files are stored in R2 under:

```text
data/shuber.json
media/audio/...
media/covers/...
```

The browser reads `/media/...` directly from R2 through the Worker, so the Go application is not used as the audio-file origin.

## Local development

Go-only:

```bash
go run ./cmd/server
```

Cloudflare Worker + Container:

```bash
npm run dev
```

The latter needs Docker for the local Container.

## Configuration

Important values in `wrangler.jsonc`:

- `MEDIA_BUCKET`: R2 bucket binding
- `STORAGE_BACKEND=r2`
- `R2_INTERNAL_URL=http://r2.internal`
- `DATA_KEY=data/shuber.json`
- `COOKIE_SECURE=true`
- `MAX_UPLOAD_BYTES=94371840` (90 MiB application-level default)
- `SHUBER_ADMIN_LOGIN=admin`

`SHUBER_ADMIN_PASSWORD` is intentionally not stored in the repository. Set it with `wrangler secret put`.

Cloudflare account-level request body limits still apply to browser uploads, so very large audio files may require a higher zone/account upload limit.

## Validation already performed

The Go application passes:

```text
go test ./...
```

The Cloudflare files were prepared against the current Containers and R2 configuration documented by Cloudflare on September 30, 2026. A real Cloudflare deployment is not performed from this sandbox, so the final Docker image build and Cloudflare resource provisioning must be executed in your Cloudflare account.
