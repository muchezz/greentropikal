# NjiraLab

Building technology for human progress.

This repository holds the NjiraLab website and **Njira Vault**, the company's secret-sharing
product. Both are served by a single Go binary with no runtime dependencies other
than Redis.

```
NjiraLab
├── Products
│   ├── StudyZora    — AI-powered learning (presented here, built elsewhere)
│   └── Njira Vault  — secure secret sharing (built and served from this repo)
└── Technology
    ├── Cloud & infrastructure
    ├── Platform engineering
    ├── Security
    ├── AI systems
    └── Distributed systems
```

## Running it

```bash
docker compose up --build     # http://localhost:8080
```

Or directly, with a Redis instance reachable at `localhost:6379`:

```bash
go run .
```

## Routes

| Route | Purpose |
| --- | --- |
| `GET /` | Company homepage |
| `GET /vault` | Create a secret |
| `GET /s/{id}` | Read a secret (`noindex`, `no-store`) |
| `GET /robots.txt`, `GET /sitemap.xml` | Crawler metadata |
| `POST /api/create` | Create a secret, returns the link including its key fragment |
| `POST /api/check` | Report availability **without** spending a view |
| `POST /api/view` | Spend a view and return the plaintext |
| `POST /api/burn` | Destroy a secret immediately |
| `GET /api/health` | Liveness (no counts, no operational detail) |

`/secrets` and `/secret/{id}` from the previous version redirect to `/vault` and
`/s/{id}`, so links shared before the rebrand still resolve.

## Configuration

| Variable | Default | Notes |
| --- | --- | --- |
| `PORT` | `8080` | |
| `BASE_URL` | `http://localhost:8080` | Public origin. Used for canonical URLs, the sitemap, and Njira Vault links. **Set this in production.** |
| `REDIS_URL` | `localhost:6379` | Accepts `host:port` or a full `redis://` URL |
| `REDIS_PASSWORD`, `REDIS_DB` | — | Used only with the `host:port` form |
| `STUDYZORA_URL` | unset | When unset, the StudyZora call to action points at the contact section instead of inventing a destination |
| `CONTACT_EMAIL` | `hello@greentropik.com` | Carried over from the previous site — **update this to an NjiraLab address** once the domain is live |
| `ALLOWED_ORIGINS` | unset | Comma-separated origins permitted to call the API cross-origin. Unset means same-origin only; `*` is ignored on purpose |
| `TRUST_PROXY` | `false` | Set to `true` only behind a proxy that overwrites `X-Forwarded-For`, otherwise clients can spoof their rate-limit identity |
| `ENABLE_HSTS` | `false` | Set to `true` when served over HTTPS |

## How Njira Vault handles secrets

Each secret is encrypted with **AES-256-GCM** under a key generated for that one
secret. The key is returned to the creator inside the link fragment (`/s/<id>#<key>`)
and is **never written to storage** — Redis holds only ciphertext plus its type,
view counter, limit, and expiry. Browsers do not transmit the fragment, so the key
does not appear in request lines, access logs, or `Referer` headers.

Reading a secret is a three-step operation: metadata and ciphertext are read,
decryption is attempted, and only then is a view spent. A wrong or truncated key
therefore cannot burn a view the intended recipient still needs. The view counter
is incremented and the record deleted inside a single Redis script, so a one-time
secret cannot be read twice by concurrent requests.

Expiry is enforced by Redis TTL rather than by application code, and ranges from
5 minutes to 30 days. The compose file runs Redis with persistence disabled, so
secrets are never written to a disk volume.

### What Njira Vault does not do

Stated plainly, because the interface states it too:

- **This is not end-to-end encryption.** The plaintext and the key both pass
  through the server's memory while a secret is created and while it is read.
  Deploy it somewhere you would trust with the data itself.
- **Anyone holding the complete link can open the secret.** There is no
  recipient authentication; the 128-bit identifier plus the key *is* the
  credential.
- **Links persist in browser history** until the reader clears them.
- **Rate limiting is per-instance and in-memory.** Running multiple replicas
  behind a load balancer needs a shared quota at the edge as well.

### Hardening applied to the site

- Strict CSP (`default-src 'none'`, no inline script or style), `nosniff`,
  `frame-ancestors 'none'`, `Referrer-Policy: no-referrer`, `Permissions-Policy`,
  optional HSTS.
- Secret identifiers are validated against a fixed pattern and are never
  interpolated into markup — the reader page takes the id from its own URL.
- Secret text is rendered with `textContent`, never `innerHTML`.
- `Cache-Control: no-store` on every API response and on secret pages, plus
  `X-Robots-Tag: noindex, nofollow, noarchive`.
- Request bodies are capped, unknown JSON fields are rejected, and lookup
  failures return one indistinguishable message for expired, exhausted, burned,
  and never-existed secrets.
- No third-party requests: fonts, styles, scripts, and images are all served
  from this origin.

## Front end

No framework, no build step, no webfonts. `web/` holds three `html/template`
pages and the static assets; everything is embedded into the binary with
`go:embed`, fingerprinted at startup, and served with immutable caching. The
brand system lives in `web/static/njiralab.css`; Njira Vault's darker surface
treatment is layered on top in `web/static/vault.css`.

## Naming

**StudyZora** keeps its own name — it is distinctive, it names its category, and
it is a separate product with its own identity. **Njira Vault** takes the parent
prefix because "Vault" alone collides with a well-known product in the same
category; the short root travels better than the full company name.

## Project history

This repository was previously **Greentropikal**, an infrastructure consultancy
site with a secret-sharing tool attached. The git history is preserved
unchanged; the rebrand to NjiraLab is a normal commit on top of it.
