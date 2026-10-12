# Hosting Option: All-in on Cloudflare

Could meetspace run entirely on Cloudflare (compute, database, storage, CDN and custom domains) instead of a DigitalOcean droplet? This file collects what Cloudflare offers, what each product costs, how each maps to the PRD, and what is still unverified. It is written to be compared side by side with `bunny.md`. Email is evaluated as a separate service in `email.md`, so the cost totals here exclude it.

**Researched:** 2026-09-29, from developers.cloudflare.com docs, changelog and pricing pages. The pages were read through a summarizing fetch tool, so exact numbers are "per docs summary"; re-check the ones that drive the decision. Prices and beta statuses change often.

**Companion documents:** `prd.md` (requirements, §6 architecture and cost estimate), `development-plan.md` (current droplet-based plan), `bunny.md` (the other option), `email.md` (email as a separate service).

---

## Summary

| Question | Answer |
|---|---|
| Can the Go app run on Cloudflare? | Yes, as a Cloudflare Container (Docker image, GA since 2026-04-13), reachable only through a Worker and a Durable Object. Go on Workers itself (WASM) is not realistic. |
| Can Postgres run on Cloudflare? | Not self-hosted. Container disk is ephemeral (snapshots are "coming soon"), so a container database loses its data on every sleep, restart or rollout. |
| Is there a managed Postgres? | Only through a partner: PlanetScale Postgres, created from the Cloudflare dashboard and billed on the Cloudflare invoice (GA for pay-as-you-go since 2026-06-18). From about $5/month single node, $15/month for 3-node HA. Any other TLS Postgres also works with Hyperdrive. |
| Managed SQL that Cloudflare runs itself? | D1, which is SQLite (GA). 10 GB per database, automatic 30-day point-in-time restore, no interactive transactions. From Go it is only reachable over a rate-limited REST API. |
| Can we run fully serverless? | Yes, if the app is written in TypeScript: Workers, D1, Cron Triggers, Queues, R2. This is the most complete all-in-one story of the two vendors. |
| Durable job queue or cron? | Both exist: Cron Triggers (every minute), Queues, Durable Object alarms and Workflows. |
| Custom domains for paid groups? | Cloudflare for SaaS: works on the Free plan, 100 hostnames included, up to 50,000 at $0.10 each per month, free SSL. Apex domains are the weak spot. |
| Email? | Treated as a separate service (see `email.md`). Cloudflare's own Email Service is **public beta**, "intended only for transactional emails", $0.35 per 1,000 after 3,000 free, and its daily quotas are unpublished, so it is not a clean candidate yet. |
| Monthly cost (excluding email) | ~$19–31 (Go container + PlanetScale) or ~$5 (all-Workers TypeScript), versus ~$18 for the droplet plan and ~$7–8 / ~$2–5 for the two Bunny shapes. |

---

## Products

### Cloudflare Containers (compute)

Docker containers launched and addressed through Workers and Durable Objects. Status: **generally available** since 2026-04-13. Requires the Workers Paid plan ($5/month minimum).

**Pricing** (billed per 10 ms while running, on top of the $5 plan)

| Item | Included per month | Overage |
|---|---|---|
| Memory (provisioned) | 25 GiB-hours | $0.0000025 per GiB-second |
| vCPU (active usage only) | 375 vCPU-minutes | $0.000020 per vCPU-second |
| Disk (provisioned) | 200 GB-hours | $0.00000007 per GB-second |
| Egress, North America and Europe | 1 TB | $0.025/GB |
| Egress, Oceania, Korea, Taiwan | 500 GB | $0.05/GB |
| Egress, elsewhere | 500 GB | $0.04/GB |

Worker requests and Durable Object usage are billed separately, and logs are billed at Workers Logs rates. Arithmetic (ours, not from a page): an always-on `basic` instance (1 GiB, 4 GB disk) is about $6.25 of memory plus $0.70 of disk per month, plus CPU only for the time it is busy.

**Instance types**

| Type | vCPU | Memory | Disk |
|---|---|---|---|
| lite | 1/16 | 256 MiB | 2 GB |
| basic | 1/4 | 1 GiB | 4 GB |
| standard-1 | 1/2 | 4 GiB | 8 GB |
| standard-2 | 1 | 6 GiB | 12 GB |
| standard-3 | 2 | 8 GiB | 16 GB |
| standard-4 | 4 | 12 GiB | 20 GB |

Custom types: at least 1 vCPU, at most 4 vCPU, 12 GiB and 20 GB, with at least 3 GiB per vCPU. Account limits: 1,500 concurrent vCPU, 6 TiB memory, 30 TB disk, 50 GB of image storage.

**Lifecycle and addressing**

- **Addressing:** a container is reachable only through a Durable Object binding, with a Worker in front and the `@cloudflare/containers` SDK. There is no direct public endpoint and no inbound non-HTTP TCP/UDP from end users.
- **Sleep:** the default `sleepAfter` is 10 minutes of inactivity, after which the container stops. It is configurable. There is **no documented always-on or minimum-instances setting**. Keeping it warm means a long timeout plus keep-alive requests or an alarm, and instances can still be stopped by host restarts or rollouts.
- **Cold start:** 1–3 s typical, depending on image size.
- **Shutdown:** SIGTERM, then SIGKILL after up to 15 minutes.
- **Disk:** all disk is ephemeral. Each start gets a fresh disk from the image. There are no volumes. FUSE mounts of R2 work but are slow.
- **Regions:** placed on pre-initialized locations near the request. The docs do not describe pinning a region.
- **Autoscaling:** none built in (planned). Scaling is done in code.
- **Multi-container pods:** not documented. A container maps 1:1 to a Durable Object ID, so a Postgres sidecar is not an option, and would not survive anyway.
- **Extras:** SSH into live instances, Docker Hub images, active-CPU billing.

### Workers (edge compute)

| | Free | Paid ($5/month minimum) |
|---|---|---|
| Requests | 100k/day | 10M/month included, then $0.30/M |
| CPU time | 10 ms per invocation | 30M CPU-ms/month included, then $0.02/M CPU-ms |
| Max CPU per invocation | 10 ms | 5 min (default 30 s) |
| Cron and Queue consumer CPU | not stated | 15 min |
| Subrequests | 50 | 10,000 |
| Memory per isolate | 128 MB | 128 MB |
| Bundle size (uncompressed) | 64 MiB | 64 MiB |

- No egress or bandwidth charges. Static asset requests are free and unlimited.
- **Languages:** JavaScript, TypeScript, Python and Rust are first class. **Go, C, C++ and Kotlin work only through WebAssembly.** With 128 MB of memory and no native `net/http` server, a Go app with a Postgres driver would need heavy adaptation. Realistic options are a Go container behind a Worker, or writing the app in TypeScript. (Our assessment; TinyGo behavior was not verified.)
- **Outbound TCP:** port 25 is blocked. Ports 465 and 587 were not stated as blocked. Cloudflare IPs, localhost and private ranges are blocked targets.

### Scheduling and queues

| Product | What it gives us | Key numbers |
|---|---|---|
| **Cron Triggers** | Scheduled Worker runs, UTC, down to every minute | 5 per account on Free, 250 on Paid. Changes take up to 15 min to propagate. Billed as a Worker invocation. |
| **Queues** | Durable message queue with batching, retries and a dead-letter queue | 1M operations/month included, then $0.40/M. About 3 operations per message. 128 KB per message. Retention 4 days default, up to 14. Delivery semantics not stated in the docs we read (believed at-least-once). |
| **Durable Object alarms** | Per-object timers, durable | One row written per `setAlarm()`. |
| **Workflows** | Durable multi-step execution with sleeps up to 365 days | 30 s CPU per step (up to 5 min), 30-day state retention. Pricing not fetched. |

For meetspace's 5-minute reminder run, hourly cleanup and email retries, a Cron Trigger is enough. The Go container is asleep unless something wakes it, so the cron Worker has to call it (which also wakes it).

### Durable Objects and D1 (databases)

**D1** is SQLite. Status: GA. Read replication is public beta.

| Item | Fact |
|---|---|
| Paid pricing | 25B rows read/month and 50M rows written/month included, then $0.001 per million read and $1.00 per million written. 5 GB storage included, then $0.75/GB-month. No egress or compute charge. |
| Free plan | 5M rows read/day, 100k written/day, 5 GB. Enforced as hard limits from 2026-09-01. |
| Size | 10 GB per database (500 MB free), 50,000 databases on paid (10 free). |
| Query limits | 30 s per query, 100 bound parameters, 100 KB statement, 2 MB per row, 6 simultaneous connections per invocation. Each database runs one query at a time. |
| Transactions | Auto-commit only. `batch()` runs as one transaction. No interactive transactions. |
| Backups | **Time Travel**: automatic minute-level point-in-time restore, 30 days on paid, 7 on free, no extra cost. Restore is in place only; no clone to a new database. |
| Consistency | Single writer. Read replicas (beta) give sequential consistency through the Sessions API, which works from Worker bindings only. |
| SQL | SQLite with FTS5, JSON functions and math functions. |
| Access from Go | No official `database/sql` driver. Community drivers wrap the REST API, which is subject to the global Cloudflare API rate limit and meant for admin use. The recommended pattern is a proxy Worker. |

**Durable Objects with SQLite** are an alternative store: up to 10 GB per object, 30-day PITR, the same row-based pricing as D1 plus request and duration fees ($0.15/M requests, $12.50/M GB-s after the included amounts). Each object is single-threaded and reachable only through a Worker, so it means sharding the data model.

**Fit with meetspace's planned schema** (the same list as `bunny.md`, since both are SQLite):
- **Available as planned:** partial unique indexes (one owner per group), CHECK constraints, generated columns, window functions, `ON CONFLICT` upserts, FTS5 (confirmed here, unverified on Bunny), math functions (confirmed here, so haversine can run in SQL).
- **Needs a different approach:** JSONB becomes JSON text, `date_trunc` becomes `strftime`, `TIMESTAMPTZ` becomes text or an integer, and River's Postgres features go away.
- **Extra difference:** no interactive transactions, so read-modify-write flows must be one `batch()` or be written as a single statement.

### Hyperdrive and managed Postgres

- **Hyperdrive** is connection pooling and query caching in front of an existing Postgres (or MySQL), used from Workers. It runs in transaction pooling mode. Included in the Workers plans at no extra charge and with no egress fee (100k queries/day on Free, unlimited on Paid).
- **Compatible providers listed:** Neon, Supabase, PlanetScale, Fly, Xata, Nile, Timescale, pgEdge, CockroachDB, DigitalOcean, AWS RDS and Aurora, Google Cloud SQL, Azure and Prisma Postgres. Any TLS Postgres reachable over TCP appears to work, but the docs don't state it outright.
- **No Go driver is documented.** The docs are Workers-focused. A Go app in a Container would most likely connect straight to the database's own endpoint and skip Hyperdrive; the pooling benefit is lost, which matters less for one always-on Go process. (Unverified.)
- **PlanetScale Postgres via Cloudflare:** create it from the Cloudflare dashboard, which also creates a Hyperdrive config, and pay on the Cloudflare invoice. PlanetScale's own list price is about $5/month for a single node and $15/month for 3-node HA, plus storage, backup, egress and PgBouncer overages.
- **Neon and Supabase** appear only as generic Hyperdrive-compatible providers. No billed-through-Cloudflare integration was found.

### R2 (object storage)

| Item | Fact |
|---|---|
| Storage | Standard $0.015/GB-month. Infrequent Access $0.01/GB-month with a 30-day minimum and retrieval fee. |
| Operations | Class A (writes, lists) $4.50 per million. Class B (reads) $0.36 per million. Deletes are free. |
| Egress | **Free.** |
| Free tier | 10 GB-month, 1M Class A and 10M Class B operations per month. |
| S3 compatibility | GA. Endpoint `https://<ACCOUNT_ID>.r2.cloudflarestorage.com`, region `auto`. Multipart, CopyObject, CORS and lifecycle rules work. Versioning, object lock, tagging, ACLs and bucket policies do not. |
| Presigned URLs | GET, HEAD, PUT and DELETE, expiry 1 s to 7 days, on the S3 domain only (not custom domains). |
| Public access | Custom domains on a zone in the same account (up to 100 per bucket) give caching, WAF and Access. `r2.dev` is rate-limited and for development only. |
| Limits | 5 TiB per object, 5 GiB per single-part upload, 1 write per second to the same key. |

Versus Bunny Storage ($1 minimum, S3 API in preview) and DO Spaces ($5 for 250 GiB): R2 is GA, S3-compatible with presigned URLs, has no minimum and no egress fees. At meetspace's expected size it costs cents.

### Cloudflare for SaaS (custom domains) and CDN

**Custom hostnames**

| Item | Fact |
|---|---|
| Plans | Works on Free, Pro, Business and Enterprise. |
| Included | 100 hostnames. |
| Extra hostnames | $0.10 per hostname per month (month basis from a third-party summary, not the docs page). |
| Maximum | 50,000 on Free, Pro and Business (hard cap). Enterprise is unlimited. The 5,000 to 50,000 change is dated 2025-05; a "July 2026" price-cut claim in search summaries could not be confirmed. |
| Certificates | Free, issued per hostname (ECDSA plus RSA fallback). Validation by HTTP, TXT, or delegated `_acme-challenge` CNAME, which can issue and renew before the customer cuts over. |
| Fallback origin | A proxied DNS record in our zone. Customers CNAME to a friendly target that points at it. |
| Custom origin per hostname | Available on all plans. |
| Visitor hostname at the origin | The customer's hostname arrives as the Host header by default. |
| API | `POST/GET/DELETE /zones/{zone}/custom_hostnames`, plus a quota endpoint. |
| Enterprise only | Custom certificates, mTLS, wildcard hostnames, choosing the CA. |
| Non-SNI clients | Not on Free. |

- **Apex domains are the weak spot.** A customer can only point an apex at us if their DNS provider flattens CNAMEs. A records to us need "apex proxying", which is Enterprise, priced through the account team, with optional BYOIP. This is the same constraint the PRD already accepts for Bunny (owners use a subdomain).
- **Terms:** SSL for SaaS is only to secure our customers' domains inside our app. We may not give customers dashboard or API access or resell it. meetspace fits.
- **Cloudflare-to-Cloudflare:** customers whose own zone is on Cloudflare may hit restrictions pointing at our target. Not verified.

**Cache**

- HTML is **not cached by default.** A Cache Rule ("Cache Everything") turns it on, and responses with `Set-Cookie` are not cached. Cache-key customization is limited on Free, Pro and Business (richer on Enterprise). Host is normally part of the key (standard behavior, not confirmed from the docs excerpt).
- **Purge** by URL, hostname, cache tag, prefix or everything is available on **all plans**. Rate limits:

  | Plan | Hostname, tag, prefix, everything | Single-URL purge |
  |---|---|---|
  | Free | 5 per minute (bucket 25) | 800 URLs/s |
  | Pro | 5 per second (bucket 25) | 1,500 URLs/s |
  | Business | 10 per second (bucket 50) | 1,500 URLs/s |
  | Enterprise | 50 per second (bucket 500) | 3,000 URLs/s |

  Every purge call is limited to 100 items on Free, Pro and Business. A 200 response does not guarantee eviction.
- **What this means for the PRD:** purging every affected page on every hostname is done with single-URL purge, which is generous on Free. Tag or hostname purge per change would hit the 5-per-minute cap on Free.
- **Not verified:** whether purge reaches cached pages served under a SaaS custom hostname (it is scoped to the zone of the API call, so it should). Test before committing.
- Smart Tiered Cache is free. Cache Reserve is paid. Request coalescing (cache lock) is on by default. The Workers Cache API is per data center only and doesn't replicate.

**Bandwidth and terms**

- CDN bandwidth is unmetered by reputation, but no page we could fetch says so explicitly.
- The service terms let Cloudflare limit the CDN if a site serves video or "a disproportionate percentage of pictures, audio files, or other large files" without a paid media product. HTML and text are fine. **User uploads should be served from R2**, not through the CDN cache of the main zone.

**Security**

| Feature | Free | Pro | Business |
|---|---|---|---|
| DDoS | Unmetered L3–L7 | same | same |
| Managed WAF | Free ruleset | + Cloudflare Managed and OWASP | same as Pro |
| Rate limiting rules | 1 rule, 10 s period, per IP | 2 rules, 1 min | 5 rules, 10 min |
| Turnstile | Free, 20 widgets, 10 hostnames each | — | — |

For custom hostnames, WAF and rate limits apply through "WAF for SaaS". Plan gating was not verified. All rules and counts live on our single SaaS zone, so they are shared across tenants. Turnstile's any-hostname widgets are Enterprise only.

### Email (Cloudflare Email Service)

Email is evaluated as a separate service in `email.md`, which compares 14 providers and treats Cloudflare's as a gated candidate. This section keeps the Cloudflare-specific product facts. Two products share the name:

- **Email Sending: public beta** since 2026-04-16 (private beta from November 2025). The docs landing page still says "Beta". No GA date found. Requires Workers Paid. Free accounts can only send to verified destination addresses in their own account.
- **Email Routing (inbound): GA**, free on all plans, unlimited. Rules go to an address or to a Worker's `email()` handler. Useful for a `reply@` or support inbox, not required by the PRD.

**Sending**

| Item | Fact |
|---|---|
| Methods | Workers `send_email` binding, REST (`POST /accounts/{id}/email/sending/send`, bearer token), SMTP at `smtp.mx.cloudflare.net:465` (implicit TLS only, beta, user `api_token`). REST and SMTP both work from Go. |
| Price | 3,000 emails/month included, then **$0.35 per 1,000**. Rejected and suppressed sends don't count. |
| Limits | 50 recipients per message, 5 MiB per message, 998-char subject, 16 KB of headers. Domains per zone: 30. |
| Daily quota | New accounts start with "conservative daily quotas that automatically increase". **The numbers are not published**, nor is burst behavior. A limit-increase form exists. Rate limiting returns HTTP 429. |
| Bulk | No batch endpoint documented. With per-recipient unsubscribe links, expect one call per member. |
| Domain setup | Onboarding writes SPF, DKIM, DMARC and MX records (bounces use a `cf-bounce` subdomain). Subdomains work. Whether the zone must be on Cloudflare DNS is not clear from the docs. |
| Dedicated IPs | Not mentioned. Reputation is shared ("managed infrastructure"). Resend sells dedicated IPs at $30/month. |
| Sender display name | Supported: `from` takes `{ email, name }`. Confirmed for the Workers API; verify on REST with a test call. A shared platform domain with a per-group display name is feasible. |
| One-click unsubscribe | Supported. `List-Unsubscribe` (HTTPS or mailto) and `List-Unsubscribe-Post: List-Unsubscribe=One-Click`, both DKIM-signed. `Message-ID` is platform-controlled. |
| Suppression | Automatic. Complaints are permanent, hard bounces last 7 days (permanent if the mailbox doesn't exist), soft bounces 24 h. Scope is per account or per sending domain (per-domain added 2026-09-25). Bulk import is limited to 10 per minute. |
| Events | `delivered`, `deferred`, `bounced`, `failed`, `rejected`, `complained`, delivered **through Cloudflare Queues only, not webhooks**. The Go app has to pull from the queue over HTTP, or a small Worker consumer forwards them to our endpoint. |
| Logs | Dashboard activity log for 30 minutes to 30 days, with SPF/DKIM/DMARC results and the recipient server's response. Email preview retained about 7 days. GraphQL metrics API. |

**Caveats that matter for meetspace**

- The FAQ says: "Email Service is intended only for transactional emails. We plan to support marketing emails and bulk sender tooling in the future." Magic links, RSVP confirmations and reminders are clearly transactional. **"New meeting" announcements and organizer custom messages to hundreds of members are a gray area.** Confirm with Cloudflare.
- Beta means no SLA, and final pricing was described as "pending" during the private beta.
- The unpublished daily quota is the biggest risk for magic-link deliverability and for a burst of a few thousand meeting emails.
- CAN-SPAM, GDPR and CASL compliance and proper unsubscribe handling are required by the docs. The PRD already provides one-click unsubscribe.

**Versus other providers.** See `email.md`. In short: at 50k emails a month Cloudflare costs about $21.45 including the $5 Workers Paid plan (the $5 is shared with everything else), against $5 for Amazon SES and $20 for Resend. Its remaining problems are the beta status, the transactional-only terms, unpublished quotas, events available only through Queues, and Cloudflare's own `Feedback-ID` header.

### Also worth knowing

- **Cloudflare Tunnel:** `cloudflared` makes outbound-only connections to an origin, so no open ports are needed. It would let a plain droplet sit behind Cloudflare without exposing a public origin. Whether a tunnel hostname can be the fallback origin for SaaS hostnames is unverified. Believed free; pricing not confirmed.
- **Workers VPC:** beta, free during the beta, reaches private services through Tunnel.
- **Load Balancing:** about $5/month per third-party sources. The official page was unavailable.

---

## Deployment Shapes for meetspace

### A. Worker + Go Container + PlanetScale Postgres (keeps Go and the current design)

```
Visitor ──▶ Cloudflare (SaaS custom hostnames, free SSL, cache rules)
              └─ fallback origin: Worker ──▶ Durable Object ──▶ Go Container (basic, 1 GiB)
                                                                  ├─ Postgres ──▶ PlanetScale (via Cloudflare billing)
                                                                  └─ jobs: River on Postgres
Cron Trigger (every minute) ──▶ Worker ──▶ POST /tasks/tick on the container (also keeps it awake)
Uploads ──▶ R2 (S3 API, presigned URLs)
Email ──▶ separate service (see email.md)
```

- **What stays as planned:** Go, Postgres, the planned schema, River, Stripe and Google Maps.
- **What goes away:** the droplet, Caddy, origin TLS, OS patching, the firewall and self-run Postgres backups (PlanetScale handles them).
- **What is new:** requests reach Go through a Worker and a Durable Object, and the container sleeps after 10 idle minutes unless kept warm. Container restarts drop in-memory state, so the goroutine tickers in the development plan must be idempotent, as they already are (`reminder_sent_at`, deleting expired rows). Cold starts add 1–3 s to the first request after a sleep.
- **Worth testing first:** the Worker in front of the container must forward the visitor's hostname, and Cloudflare-side caching must work with a Worker as the fallback origin.

### B. All-Workers (TypeScript)

```
Visitor ──▶ Cloudflare (SaaS custom hostnames) ──▶ Worker (TS) ──▶ D1 (SQLite)
Cron Trigger ──▶ same Worker (reminders, cleanup)     Queues ──▶ email sends and retries
Uploads ──▶ R2
```

- **Different plan:** the app is written in TypeScript and the schema in SQLite's dialect. Nothing has been built yet, so this changes the development plan, not existing code.
- **Durable email:** write an outbox row with the change in one `batch()`, then push a Queue message. The queue consumer sends and retries.
- **Maturity:** GA products throughout (email is a separate service, see `email.md`), except D1 read replication, which is not needed.
- **Constraints:** 10 GB per D1 database. That is far above the expected size, but a single database is a single writer. Sharding per group is possible (50,000 databases allowed) but is a redesign.

### C. Go on the droplet, Cloudflare in front only

This is the current plan with Cloudflare replacing Bunny as the CDN and custom-domain layer: SaaS custom hostnames pointing at the droplet (optionally through a Tunnel), R2 for uploads. It gets the hostname capacity and the free CDN without moving compute. It is the smallest change and worth pricing as a baseline.

---

## Cost Estimates (monthly)

Assumptions:
- Launch-scale traffic, under 100 custom hostnames (no per-hostname fees), CDN on the Free plan.
- Go container `basic` (1 GiB, 1/4 vCPU), always kept warm, averaging 5–10% of one core: memory about $6.25, disk about $0.70, CPU about $1.50–3.50 after the included amounts.
- PlanetScale at $5 (single node) to $15 (HA).
- R2 at about 20 GB of uploads, roughly $0.15 and well inside the free operations.
- Email and SMS are excluded (separate services: `email.md`, `sms.md`).

| | Droplet plan (PRD §6) | A. Container + PlanetScale | B. All-Workers | Bunny A / B (for reference) |
|---|---|---|---|---|
| Workers Paid plan | — | $5 | $5 | — |
| Compute | $12 | ~$8.50–10.50 | included | ~$4.50 / ~$0.22–1 |
| Database | included | $5–15 | included (D1) | $1 / $0 in preview |
| Object storage | $5 | ~$0.15 | ~$0.15 | $1 |
| CDN and custom domains | ~$1 | $0 (Free plan) | $0 | ~$1 |
| Scheduler | included | included (Cron) | included (Cron) | included / $0–2 |
| **Total, excluding email** | **~$18** | **~$19–31** | **~$5** | **~$7–8 / ~$2–5** |

Cloudflare's Workers plan is shared by every product, so the $5 is paid once (and if Cloudflare's own email service were used, it would already be covered). Shape A is not cheaper than the droplet, because container memory is billed per GiB-second while running, and PlanetScale adds a database bill. Shape B is the cheapest but requires the TypeScript rewrite. The earlier totals that included email were Resend at $20 (~$38, ~$39–51, ~$25) or Cloudflare's own service at ~$16.45; that line is now in `email.md`.

---

## Risks

1. **Go on Containers (A):** no always-on setting, ephemeral disk, restarts on rollouts, no region pinning, no autoscaling, and every request goes through a Worker and a Durable Object. Latency and reliability under real traffic are untested.
2. **Postgres is a partner product (A):** Cloudflare doesn't run it. Hyperdrive is documented only for Workers, so the Go container connects directly and the connection path and pricing are PlanetScale's.
3. **D1 fit (B):** SQLite, no interactive transactions, one writer per database, 10 GB cap. Reaching D1 from anything but Workers is rate-limited REST.
4. **Cloudflare's own email (if chosen):** Email Sending is beta, transactional-only by policy, has unpublished daily quotas and delivers events only through Queues. Not needed if another email provider is used (see `email.md`).
5. **Apex domains:** customers need CNAME flattening or Enterprise apex proxying, same as Bunny.
6. **Free-plan terms:** the CDN terms limit large media on the free plan, so uploads have to be served from R2. Purge on Free is 5 bulk requests per minute, so per-URL purging is the practical path.
7. **Lock-in (B):** Workers, D1, Queues and Durable Object APIs are Cloudflare-specific. A Go container on a Postgres schema (A) moves elsewhere far more easily.
8. **Cost visibility:** container memory, CPU and disk are metered separately, so the bill is harder to predict than a flat droplet.

## Open Questions (verify before choosing)

1. Can a Worker fronting a Go container keep the visitor's hostname (Host header) intact through the Durable Object hop, and can a Worker be the SaaS fallback origin?
2. Does Cloudflare's cache (Cache Rules, purge by URL) sit in front of a Worker fallback origin for SaaS hostnames, and does URL purge work for custom-hostname URLs?
3. Can a Container be kept reliably warm with a cron plus keep-alive, and how often are containers restarted in practice (host restarts, rollouts)?
4. Does a Go container reach PlanetScale Postgres directly with good latency, and does Hyperdrive have any non-Worker endpoint?
5. (Only if Cloudflare's own email is considered; see `email.md`.) What are Email Sending's real daily quotas and burst limits for a new account, and how quickly are increases granted?
6. Does Cloudflare accept meeting announcements and organizer messages as "transactional"?
7. Does Email Sending require the sending domain's DNS on Cloudflare?
8. Do REST sends accept `{ email, name }` for `from`?
9. What is the D1 REST API rate limit, and what does a Go client experience?
10. Queues delivery semantics (at-least-once assumed) and Workflows pricing.
11. Is the "July 2026" custom-hostname price change real, and what is the current per-hostname price?
12. Does WAF for SaaS apply on the Free plan?
13. Can a Cloudflare Tunnel hostname act as the SaaS fallback origin (needed for shape C without open origin ports)?
14. How does a customer whose own DNS is on Cloudflare point a hostname at our target?

## Comparing with bunny.net

The same criteria as `bunny.md`, with the Cloudflare answers filled in:

| Criterion | Cloudflare | bunny.net |
|---|---|---|
| Monthly cost at launch scale (excluding email) | ~$19–31 (Go) or ~$5 (TS) | ~$7–8 (container) or ~$2–5 (edge) |
| Runs the Go binary unchanged? | In a Container, behind a Worker and Durable Object; no always-on setting | In a Magic Container, always on, native pull-zone origin |
| Postgres | Not self-hosted; PlanetScale via Cloudflare billing | Self-hosted in a container on a single non-replicated volume; you own backups |
| Database durability | PlanetScale managed (HA at $15); D1 has 30-day Time Travel | Volume has no replication or backups; Bunny Database (preview) can lose up to 10 s |
| Durable jobs and scheduled work | Cron Triggers, Queues, alarms, Workflows built in | None; in-container goroutines or an external timer |
| Custom domains and SSL | 100 free, up to 50,000 at $0.10; apex needs flattening or Enterprise | 500 per account; apex needs flattening |
| Visitor hostname at the app | Host header by default; Worker hop still to verify | Forward Host Header (to verify) |
| Object storage | R2: GA, S3-compatible, free egress, presigned URLs | Bunny Storage: S3 API in preview |
| Email | Separate service (`email.md`); Cloudflare's own is beta, transactional only | Separate service; no bunny.net product |
| Maturity | Mostly GA; D1 read replicas beta | Mostly GA; Bunny Database, Storage S3 API preview |
| Lock-in | High for TypeScript on Workers and D1; low for Go on Postgres | Similar |
| Ongoing operations | No OS patching, no TLS work; container and Worker deploys | No OS patching, no TLS work; you own Postgres backups |

---

## Sources

- Containers: [overview](https://developers.cloudflare.com/containers/), [pricing](https://developers.cloudflare.com/containers/pricing/), [limits](https://developers.cloudflare.com/containers/platform-details/limits/), [architecture](https://developers.cloudflare.com/containers/platform-details/architecture/), [FAQ](https://developers.cloudflare.com/containers/faq/), [GA changelog (2026-04-13)](https://developers.cloudflare.com/changelog/post/2026-04-13-containers-sandbox-ga/)
- Workers: [pricing](https://developers.cloudflare.com/workers/platform/pricing/), [limits](https://developers.cloudflare.com/workers/platform/limits/), [languages](https://developers.cloudflare.com/workers/languages/), [TCP sockets](https://developers.cloudflare.com/workers/runtime-apis/tcp-sockets/), [Cache API](https://developers.cloudflare.com/workers/runtime-apis/cache/)
- Scheduling: [Cron Triggers](https://developers.cloudflare.com/workers/configuration/cron-triggers/), [Queues pricing](https://developers.cloudflare.com/queues/platform/pricing/), [Queues limits](https://developers.cloudflare.com/queues/platform/limits/), [Workflows limits](https://developers.cloudflare.com/workflows/reference/limits/), [Durable Objects pricing](https://developers.cloudflare.com/durable-objects/platform/pricing/), [limits](https://developers.cloudflare.com/durable-objects/platform/limits/), [SQLite storage API](https://developers.cloudflare.com/durable-objects/api/sqlite-storage-api/)
- D1: [pricing](https://developers.cloudflare.com/d1/platform/pricing/), [limits](https://developers.cloudflare.com/d1/platform/limits/), [Time Travel](https://developers.cloudflare.com/d1/reference/time-travel/), [read replication](https://developers.cloudflare.com/d1/best-practices/read-replication/), [SQL statements](https://developers.cloudflare.com/d1/sql-api/sql-statements/), [query API](https://developers.cloudflare.com/api/resources/d1/subresources/database/methods/query/), [free-tier enforcement (2026-09-01)](https://developers.cloudflare.com/changelog/post/2026-09-01-d1-free-tier-limit-enforcement/)
- Hyperdrive and Postgres: [pricing](https://developers.cloudflare.com/hyperdrive/platform/pricing/), [how it works](https://developers.cloudflare.com/hyperdrive/concepts/how-hyperdrive-works/), [connect to Postgres](https://developers.cloudflare.com/hyperdrive/configuration/connect-to-postgres/), [providers](https://developers.cloudflare.com/hyperdrive/examples/connect-to-postgres/postgres-database-providers/), [PlanetScale billing through Cloudflare (2026-06-18)](https://developers.cloudflare.com/changelog/post/2026-06-18-planetscale-databases-cloudflare-billing/), [blog](https://blog.cloudflare.com/deploy-planetscale-postgres-with-workers/), [PlanetScale pricing](https://planetscale.com/pricing)
- R2: [pricing](https://developers.cloudflare.com/r2/pricing/), [limits](https://developers.cloudflare.com/r2/platform/limits/), [S3 API](https://developers.cloudflare.com/r2/api/s3/api/), [presigned URLs](https://developers.cloudflare.com/r2/api/s3/presigned-urls/), [public buckets](https://developers.cloudflare.com/r2/buckets/public-buckets/)
- Cloudflare for SaaS: [plans](https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/plans/), [quotas and billing](https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/quotas-and-billing/), [getting started](https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/start/getting-started/), [custom origin](https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/start/advanced-settings/custom-origin/), [apex proxying](https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/start/advanced-settings/apex-proxying/), [security](https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/security/), [pay-as-you-go changelog (2025-05-19)](https://developers.cloudflare.com/changelog/post/2025-05-19-paygo-updates/), [service-specific terms](https://www.cloudflare.com/service-specific-terms-application-services/)
- Cache and security: [default cache behavior](https://developers.cloudflare.com/cache/concepts/default-cache-behavior/), [cache rules](https://developers.cloudflare.com/cache/how-to/cache-rules/settings/), [purge cache](https://developers.cloudflare.com/cache/how-to/purge-cache/), [Instant Purge for all](https://blog.cloudflare.com/instant-purge-for-all/), [Tiered Cache](https://developers.cloudflare.com/cache/how-to/tiered-cache/), [Cache Reserve](https://developers.cloudflare.com/cache/advanced-configuration/cache-reserve/), [DDoS](https://developers.cloudflare.com/ddos-protection/), [rate limiting](https://developers.cloudflare.com/waf/rate-limiting-rules/), [Turnstile plans](https://developers.cloudflare.com/turnstile/plans/)
- Tunnel and VPC: [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/), [Workers VPC](https://developers.cloudflare.com/workers-vpc/)
- Email: [overview](https://developers.cloudflare.com/email-service/), [pricing](https://developers.cloudflare.com/email-service/platform/pricing/), [limits](https://developers.cloudflare.com/email-service/platform/limits/), [event subscriptions](https://developers.cloudflare.com/email-service/platform/event-subscriptions/), [FAQ](https://developers.cloudflare.com/email-service/reference/faq/), [headers](https://developers.cloudflare.com/email-service/reference/headers/), [deliverability](https://developers.cloudflare.com/email-service/concepts/deliverability/), [suppressions](https://developers.cloudflare.com/email-service/concepts/suppressions/), [domains](https://developers.cloudflare.com/email-service/configuration/domains/), [send bindings](https://developers.cloudflare.com/email-service/configuration/send-bindings/), [REST API](https://developers.cloudflare.com/email-service/api/send-emails/rest-api/), [SMTP](https://developers.cloudflare.com/email-service/api/send-emails/smtp/), [Workers API](https://developers.cloudflare.com/email-service/api/send-emails/workers-api/), [changelog](https://developers.cloudflare.com/changelog/product/email-service/), [public beta (2026-04-16)](https://developers.cloudflare.com/changelog/post/2026-04-16-email-sending-public-beta/), [announcement](https://blog.cloudflare.com/email-service/), [Resend pricing](https://resend.com/pricing)
- Docs index used for searching: [developers.cloudflare.com/llms.txt](https://developers.cloudflare.com/llms.txt)
