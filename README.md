# NjiraLabs

Building technology for human progress.

This repository holds the NjiraLabs website and **Njira Vault**, the company's secret-sharing
product. Both are served by a single Go binary with no runtime dependencies other
than Redis.

```
NjiraLabs
├── Products
│   ├── StudyBora    — AI-powered learning (presented here, built elsewhere)
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
| `STUDYBORA_URL` | unset | When unset, the StudyBora call to action points at the contact section instead of inventing a destination |
| `CONTACT_EMAIL` | `hello@njiralabs.com` | Address shown in the contact section and used by the contact form's `mailto:` |
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
`go:embed`, fingerprinted and gzipped at startup, and served with immutable
caching. The system lives in `web/static/njiralab.css`; Njira Vault adds only
what a working interface needs in `web/static/vault.css`.

The palette is light and colour has semantic ownership:

| | Colour | Where it may appear |
| --- | --- | --- |
| NjiraLabs | green `#16A34A` | company chrome, navigation, primary calls to action |
| StudyBora | purple `#7C3AED` | inside the StudyBora product only |
| Njira Vault | orange `#F59E0B` | inside Njira Vault only, plus the journey's arrival marker |

Products own their accent by rebinding `--accent` on `.product--studybora` /
`.product--vault` (and `body.vault`), so nothing inside names purple or orange
directly and neither can leak into the rest of the page. The rebinding sets
`--accent`, `--accent-deep` and `--accent-tint` together; missing one is how
Njira Vault's buttons silently stayed green for a while. Where a product colour
has to carry text it uses its `-dark` step (`#6D28D9`, `#B45309`) to clear AA;
the display values stay on fills. Bright green `#22C55E` is 2.5:1 on white and
is confined to the hero gradient and the mark.

Type is Inter, self-hosted as a single variable `woff2` covering 100–900 across
latin (48 KB), so the page still makes no third-party requests.

Greys are picked against contrast rather than taste: `--body` `#666666` is
5.7:1 on white and 5.3:1 on the panel, and `--muted` `#707070` is the lightest
grey that still clears AA on the panel background it has to sit on.

The homepage follows a restrained, light system: a 68px translucent header
that blurs the page behind it, a 1240px measure with 40px gutters, and #EBEBEB
hairlines doing the dividing work that borders and shadows would otherwise do.
Shadows are effectively absent except beneath product screenshots.

Two decisions carry the feel. **Every control is a pill** — 44px for the
page's primary calls to action, 36px inline — while surfaces stay rectangular
with generous corners: 28px on the product panel, 20px on cards, 16px on
inner bars, 12px on screenshots and text inputs. And **display type sits at
weight 500, not 800**: at 64px the letterforms carry the emphasis on their
own, and extra weight only makes them shout.

**The hero is split.** The statement holds the left at up to 4rem; the
sentence that explains it sits opposite rather than beneath, so neither column
has to carry the full measure — the headline can run large without the prose
running long. Below 820px it stacks to statement, explanation, actions.

**Under the hero is the product panel.** A washed 28px surface holding the
real Njira Vault interface in its own chrome, a label-caps chip naming the
product, and a bar of properties the implementation actually has
(`AES-256-GCM`, key never stored, expiry on a timer). The wash is a soft
radial in the product's own colour. Phones are served the product's phone
layout rather than the desktop one shrunk past legibility.

There is deliberately **no logo wall and no metrics strip**. Both would have
to be invented.

### Brand assets

The icons and favicons come from the supplied NjiraLabs brand pack and are used
unmodified: `mark.svg` (green, company), `mark-studybora.svg` (purple),
`mark-vault.svg` (orange), plus `favicon.svg`, `favicon.ico` and the icon PNGs.

The wordmark is set in Inter in HTML rather than used as an image. The pack's
horizontal lockup draws its text with an SVG `<text>` element in Inter, which
falls back to Arial on machines without Inter installed and renders
inconsistently; setting it in HTML also keeps it crisp and selectable. The wordmark is set
lowercase and two-tone in HTML (`njira` in ink, `labs` in violet) rather than as
an image, so it stays crisp and selectable; the supplied horizontal lockup is
not used in the navigation because its tagline is illegible at that size.

### Product imagery

The Njira Vault images on the homepage are screenshots of the real running
product, captured at two widths so the interface stays readable on phones as
well as desktops. Regenerate them after any change to the Vault UI:

```bash
BASE_URL=https://njiralab.com go run .          # in one shell
node tools/capture-product-shots.mjs            # in another (needs Playwright)
```

**StudyBora has no screenshot in this repository.** Rather than mock up an
interface we do not have, that section falls back to a typographic treatment.
Drop a real capture at `web/static/studybora.png` and it is picked up
automatically on the next build — no template change needed.

## Naming

**StudyBora** keeps its own name — it is distinctive, it names its category, and
it is a separate product with its own identity. **Njira Vault** takes the parent
prefix because "Vault" alone collides with a well-known product in the same
category; the short root travels better than the full company name.

## Project history

This repository was previously **Greentropikal**, an infrastructure consultancy
site with a secret-sharing tool attached. The git history is preserved
unchanged; the rebrand to NjiraLabs is a normal commit on top of it.
