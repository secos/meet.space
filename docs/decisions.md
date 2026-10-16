# Decisions and Open Questions

A short record of what has been decided, why, and what is still open. Update it when something changes. The research behind each decision is in the files named below; nothing in them was tested live.

**Companion documents:** `prd.md`, `development-plan.md`, `workflows.md`, `design.md`.

---

## Decided (2026-10-12)

| Area | Decision | Why | Research |
|---|---|---|---|
| Compute | One AWS EC2 t4g.small with an Elastic IP, Dockerized (Go app, Caddy, Postgres) | Keeps the Go binary and the Postgres schema unchanged. About $16/month with the public IP. | `aws.md` (shape 1A/1B) |
| Database | PostgreSQL on the same instance, data on its own EBS volume. Nightly `pg_dump` to S3 plus daily EBS snapshots. | Cheapest. A managed database (RDS, about +$25/month) costs more than the compute. | `aws.md`, `flat-file-json.md` (database-first, files only as derived artifacts) |
| Email | Amazon SES, a la carte pricing, one region, two streams (`auth.` and `mail.`), bounces and complaints through SNS | About $0.10 per 1,000, the cheapest option that passes the must-haves (own unsubscribe headers, no tracking, per-message From name). | `email.md` |
| CDN and custom domains | Stay on BunnyCDN | No rewrite of the purge client or the custom-domain lifecycle. Bunny can front an AWS origin. | `bunny.md`, `aws.md` |
| Uploads | Private S3 bucket, served by the app on the visitor's hostname | A white-label domain must not show a storage provider's hostname. | `aws.md` |
| Visual design | Clean is the default look for every group and the platform. Any group, free or paid, can choose Lively, Confetti or Poster. Brand color and logo stay paid. Noticeboard and Program were dropped. | Clean was the preferred direction. The others give every organizer a distinct, colorful identity. The look is its own column, so it survives a lapse; color and logo reset. | Design treatments page (published artifact) |
| Not in v1 | SMS, web push, recurring meetings, cross-domain single sign-on, per-group sending domains | Cost and compliance for SMS; scope for the rest. | `sms.md`, `prd.md` §9 |

## Working decisions (2026-10-15, not finalized)

| Area | Decision | Why | Notes |
|---|---|---|---|
| Referrer-Policy on token pages | `same-origin`, not `no-referrer`, on `/auth/verify/{token}`, `/rsvp/cancel/{token}` and `/unsubscribe/{token}` (replaces the value in `development-plan.md` route category 4 and `prd.md` §6). | Under `no-referrer` a browser sends the page's own form POST with `Origin: null` and no Referer, so Rule 5's Origin check would refuse the confirm buttons and no magic-link login could complete. Accepting `Origin: null` would switch CSRF protection off (any page can make a browser send it) and reopen login CSRF. `same-origin` sends the token URL only back to our own site, which already has the token; other sites, links and map embeds never see it. | Owner chose option A on 2026-10-15. Alternatives rejected: exempting token routes from the Origin check (login CSRF), a second CSRF-token mechanism. |
| Origin check (Rule 5) details | On every POST and DELETE not declared exempt: if `Origin` is present its host[:port] must equal the current host (`null` fails); if it is absent the `Referer`'s host[:port] must; if **neither header is present the request is refused**. Only host and port are compared, never the scheme (TLS ends at the CDN and Caddy). Comparison is by parsed host equality, never prefix or substring. A refused request gets `403 Forbidden`, `Cache-Control: no-store, private`, and the same plain-text body on every host with no platform or group name ("This request didn't come from this site. Go back, reload the page and try again."); the app logs a warning with host, method, path, Origin and Referer. | Literal reading of Rule 5 ("the origin must equal the current hostname"), failing closed. Every current browser sends `Origin` on POST/DELETE, so refusing header-less requests costs real users nothing. | Owner confirmed refusing header-less requests on 2026-10-15. The one-click unsubscribe POST from mail apps needs its own exemption (Phase 6 question). |
| Client IP (Rule 8) | The app trusts a forwarded header only when its immediate peer is in `TRUSTED_PROXIES` (Caddy's address; empty in dev, so dev always uses the peer). It reads only `CLIENT_IP_HEADER` (default `X-Forwarded-For`) and ignores every other forwarding header. It takes the **rightmost address in that header that is not itself a trusted proxy**. A missing, empty or malformed header falls back to the peer address. | Every production request reaches the app through Caddy, and ports 80/443 are open to direct callers (SNS, Stripe, attackers), so "from the origin proxy" alone can't tell a CDN-set header from a forged one. The leftmost entry is whatever the caller wrote; the rightmost untrusted entry is what our own infrastructure recorded. | Owner agreed 2026-10-15. Caddy's part (trusting Bunny's published edge ranges so a direct caller can't inject an address) is settled in the Phase 9 spike with live Bunny, like `HOST_SOURCE`. |
| Host matching | The visitor hostname is lowercased, then must equal `PUBLIC_HOST`, `ORIGIN_HOST` or an active custom domain exactly, port included (`localhost:8080` in dev). A trailing dot (`meet.space.`) or an explicit default port (`meet.space:443`) is not tidied up: it gets the same 404 as an unknown host. | Hostnames are case-insensitive (DNS), and browsers already lowercase them. Browsers treat `meet.space.` as a different site (own cookie jar, different `Origin`), so accepting it would give a half-working copy where logins don't stick and forms are refused. | Owner agreed 2026-10-15. |
| `HOST_SOURCE` header edge cases | When `HOST_SOURCE` names a header: present once with a single value → use it (lowercased, matched exactly); missing → fall back to `Host`; repeated, or containing a comma → unknown host, 404. When `HOST_SOURCE=host`, `X-Forwarded-Host`, `Forwarded` and every other host-ish header are ignored. | SNS and Stripe call `origin.meet.space` directly, without the CDN header, and must still reach `/webhooks/*` and `/health`. In header mode Caddy serves only the origin site block, so the fallback can only resolve to the origin. Several values can't be trusted, so the app doesn't guess. | Owner agreed 2026-10-15. Which mode is used comes from the Phase 9 spike. |
| `/health` | Served on `ORIGIN_HOST` only (404 on the platform, custom domains and unknown hosts). `200`, plain text `ok` when the database ping succeeds within 2 seconds; otherwise `503`, plain text `database unavailable`, with the error only in the log. Always `no-store, private`. | Monitoring calls the origin directly. Serving it on every domain would expose a database-status signal on white-label domains for no user benefit. | Owner agreed 2026-10-15. A container-internal health check (Phase 12) must send `Host: origin.meet.space`. |
| Error pages | **Platform:** platform layout; 404 "We can't find that page." with "Go to the home page" → `/`; 500 "Something went wrong on our side." with "Try again". **Active custom domain:** the same, in the group's own layout (name, color, no platform marks); "unbranded" in design.md §6 is read as "no platform branding". **No site** (unknown host, `pending_dns` domain, origin paths other than `/health` and `/webhooks/*`): a bare "Page not found." with no name, no layout and no link. **htmx fragment requests:** the one-line message only, so it fits inside the box htmx 4 swaps it into. Always the right status, `no-store, private`, no stack traces or error details (log only). | One message, one way out (design.md §6); white-label (prd.md §4.3). A pending domain is the customer's own hostname, so the platform name must not appear there either. | Owner agreed 2026-10-15. |
| HTMX version | htmx 4.0.0, vendored as `static/js/htmx.min.js` | Owner preference. Pages no longer go into a `localStorage` history cache (htmx 4 reloads the full page on back), so private fragments are never written to storage. | npm still tags 2.x as `latest`; 4.0.0 is the GitHub "latest" release. The plan's htmx patterns are written for htmx 2. In htmx 4: attribute inheritance must be explicit (`hx-target:inherited`); 4xx/5xx responses swap by default (use 422 for inline form errors, `hx-status:` per element); `hx-delete` no longer sends the enclosing form's inputs; event names are colon-separated (`htmx:after:swap`). `HX-Trigger` responses and `hx-trigger="load, rsvp-changed from:body"` work unchanged. |

**Consequences of the choices above**
- No high availability. An instance failure means restoring from the latest backup; the worst case is losing up to a day of data until WAL archiving is added.
- AWS bills for the public IPv4 address ($3.65/month) whether or not it is in use.
- SES enforcement is account-wide: a bounce or complaint problem in one group can threaten sending for every group. The plan therefore pauses bad groups automatically and keeps login mail on its own stream.
- SNS and Stripe call the origin directly, so ports 80/443 can't be limited to the CDN's IP addresses.

---

## Open: must be settled before the phase named

| # | Question | Needed by | Notes |
|---|---|---|---|
| 1 | Does Bunny send the visitor's hostname as `Host`, or in another header? Does the cache key include the hostname? | Phase 9 spike (config only until then) | Documented in `development-plan.md` Phase 9. |
| 2 | Will AWS grant SES production access, and with what daily quota? | Before real sends (start now) | Manual review per region, up to 24 hours for a first reply, can be declined without detail. Fallback: Resend behind the same `Sender` interface. |
| 3 | Legal entity and Stripe account: who is the seller, in which country? | Phase 10 | Also decides Terms and Privacy wording. |
| 4 | Sales tax or VAT on the $20/year subscription: use Stripe Tax, or not collect? | Phase 10 | Not currently in the plan or the cost estimate. |
| 5 | Erasure policy. Today, deleting an account keeps its RSVPs as email-only rows so headcounts don't change. | Phase 3 (account deletion) | Alternatives: delete the email from RSVPs on account deletion and keep only the count, or keep it and say so in the Privacy page. Pick one; the flat-file research flagged the same question. |
| 6 | Product name, platform domain, and sender names. | Before Phase 0 | **Working answer (2026-10-15), not finalized:** name `meet.space`; domain `meet.space` (origin `origin.meet.space`, sending subdomains `auth.meet.space` and `mail.meet.space`); senders `login@auth.meet.space` (auth stream) and `notifications@mail.meet.space` (notify stream). All are config values (`PLATFORM_NAME`, `PUBLIC_HOST`, `ORIGIN_HOST`, `SES_FROM_*`), so a change is a config edit. DKIM, the Maps key and Stripe all use the real domain. |
| 7 | Is Turnstile needed on the anonymous RSVP form? | After launch, if abuse appears | Held behind a feature flag. |
| 10 | Which fonts and licences for Fredoka and Unbounded, and are the self-hosted files cached well on a white-label domain? | Phase 4b | Both are open-licensed on Google Fonts; confirm before bundling. |
| 8 | When does one instance with Postgres on the box stop being enough? | Revisit at roughly 200 paid groups or on the first data-loss scare | Options: WAL archiving to S3, then RDS. |

---

## Known gaps the plan accepts

- Anonymous RSVP email addresses are unverified. A typo sends a confirmation to a stranger. Mitigations are in Phase 2 (limits, MX check, no echoed free text, suppression).
- Custom domains must be added by hand to the Google Maps browser key's allowed websites until the Google API Keys API is automated (Phase 10).
- Apex domains need CNAME flattening at the customer's DNS provider. Owners are told to use a subdomain.
- Every group's complaints share one sending reputation. Per-group sending domains are deferred.
