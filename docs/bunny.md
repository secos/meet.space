# Hosting Option: All-in on bunny.net

Could meetspace run entirely on bunny.net (compute, database, storage and CDN) instead of a DigitalOcean droplet? This file collects what bunny.net offers, what each product costs, how each maps to the PRD, and what is still unverified.

**Researched:** 2026-09-29, from bunny.net's docs and pricing pages. Prices and preview statuses change often, so re-check the sources before deciding.

**Companion documents:** `prd.md` (requirements, §6 architecture and cost estimate), `development-plan.md` (current droplet-based plan), `email.md` (email is evaluated separately).

---

## Summary

| Question | Answer |
|---|---|
| Can the Go app run on bunny.net? | Yes, as a Magic Container (Docker image, always on, one instance, one region). |
| Can Postgres run on bunny.net? | Only self-hosted in a Magic Container with a persistent volume. Volumes are not replicated and not backed up, so backups become our job. |
| Is there a managed database? | Bunny Database, which is libSQL (a SQLite fork), not Postgres. It is in public preview with a 1 GB cap per database. |
| Can we run fully serverless on edge functions? | Only if the app is written in TypeScript instead of Go (the runtime is Deno), on Bunny Database, with an external scheduler. |
| Durable job queue or cron? | Neither product exists. In a container, River on Postgres works as planned. Edge functions need an outbox table plus an external timer. |
| Email? | Treated as a separate service (see `email.md`). No bunny.net product. Outbound SMTP ports are blocked by default, which doesn't matter because email providers are called over HTTPS. |
| Monthly cost (excluding email) | ~$7–8 (container + Postgres) or ~$2–5 (all-edge), versus ~$18 for the droplet plan (droplet + Spaces + CDN). |

---

## Products

### Magic Containers (compute)

Docker containers on bunny.net's own bare-metal servers, sandboxed with gVisor. Status: generally available.

**Pricing**

| Item | Price | Notes |
|---|---|---|
| CPU | $0.02 per CPU-hour | Billed on CPU actually used, not allocated |
| RAM | $0.005 per GB-hour | Billed in 64 MB increments; includes some system overhead |
| Persistent volume | $0.10 per GB-month | Billed on provisioned size, including while detached |
| Anycast IPv4 | $2/month | Only needed for non-HTTP or direct TCP access; charged even when the app is stopped |
| Egress | $0.01/GB (EU, North America); $0.03 (Asia, Oceania); $0.045 (South America); $0.06 (Middle East, Africa) | Traffic that goes out through the CDN isn't charged as egress |
| Requests | none | |

There's no free tier. A scaled-down app (one region, one instance) still accrues charges, and the dashboard shows no detailed cost breakdown.

**Limits (standard account)**

- **Per instance (pod):** 8 CPUs, 32 GiB RAM, 1 Gbps in and out, 500 outbound connections, 10 GB ephemeral disk. A pod that exceeds the ephemeral disk limit is evicted and restarted, and the data on it is lost.
- **Per account:** 20 apps. No limit on regions per app, and up to 10 instances per region per app.
- **Trial accounts:** 1 app, 1 CPU and 4 GiB per instance, 3 instances in total, 1 volume of up to 30 GB. A verified card is required even to deploy.
- **Blocked ports:** 25, 465, 587 and 2525 (mail), inbound and outbound. Support can open them.
- **CPU detection:** a container may see all of the host's cores (32 or more) but only gets 8 CPUs of time. Set `GOMAXPROCS` explicitly.

**Features**

- **Images:** from public or private registries (Docker Hub, GHCR). Deploys run through the dashboard, API, GitHub Actions or the Terraform provider.
- **Multi-container apps:** containers in one pod share localhost and can share volumes. This is how the docs pair an app with Postgres.
- **Endpoints:**
  - *CDN endpoint:* HTTP(S) through bunny's edge, with a generated `mc-<hash>.bunny.run` URL and optional sticky sessions. Server-sent events need a heartbeat at least every 30 s.
  - *Anycast endpoint:* raw TCP/UDP on an anycast IP.
- **Pull zone integration:** a pull zone can use a container as its origin (the `ComputeContainer` origin type), so our existing pull zone, custom hostnames and free SSL can sit in front of the app. The docs use this for blue/green cutovers.
- **Deploys and runtime:** health checks, rolling updates, blue/green and A/B deploys, graceful shutdown (SIGTERM plus a grace period), and a restart policy.
- **Observability:** live and historical logs (app, system, HTTP), syslog forwarding, and monitoring.
- **Config:** environment variables and secrets.
- **Regions:** 41 (12 in North America, 14 Europe, 9 Asia, 3 South America, Sydney, Johannesburg, Lagos). Deploy to one region, or let "autoprovisioning" spread the app by traffic.
- **Autoscaling:** min and max instances per region, driven by CPU. For meetspace, pin it to min = max = 1.
- **Background work:** containers can run non-HTTP background workers.

**Not found in the docs**

- Private networking between *separate* apps. The Postgres guide only shows two ways to connect: the same pod, or a public Anycast endpoint.
- A cron or scheduled-trigger feature.

### Persistent Volumes

Block storage attached to a container pod. Generally available since March 2026.

- **Price:** $0.10 per GB-month on provisioned size.
- **Size:** up to 100 GB per volume and 2 volumes per app (trial accounts: 30 GB, 1 volume). A volume can grow but never shrink.
- **Throughput:** 10 MB/s read and 10 MB/s write per volume.
- **Security:** AES-encrypted by default.
- **Durability, which matters most here:**
  - Each pod gets its **own blank volume**. Nothing is replicated between pods or regions, so a second instance means a second, empty database.
  - A volume is **bound to one machine**. If that machine is down for maintenance, the pod waits for it to come back. The only other option is to delete the volume and start empty.
  - If the machine needs a disk replacement, the volume "is recreated as a new empty volume."
  - There are **no automatic backups or replication**. The docs say to implement your own backups, for example periodic exports to object storage.
- **Scaling:** scaling down detaches volumes, which stay billed. Scaling back up may not reattach the same volume, because the pod can land on a different machine.

### PostgreSQL in a Magic Container

bunny.net publishes a guide for this setup:
- **Deployment:** single-region deployment with a single instance, using the official `postgres:17-alpine` image.
- **Volume:** mounted at `/var/lib/postgresql/data`, with `PGDATA` set to a subdirectory of it.
- **Connecting from the app:** put both containers in one pod and connect at `127.0.0.1:5432`.
- **External access:** only through an Anycast endpoint ($2/mo), which exposes the database to the internet behind its password.

Everything under Persistent Volumes applies, so Postgres here needs off-box backups. The options are `pg_dump` to Bunny Storage on a schedule, or WAL archiving (e.g. WAL-G) to cut the data-loss window to minutes.

### Bunny Database

A managed database built on libSQL (SQLite-compatible), launched around February 2026. **Status: public preview** ("features and APIs may evolve").

**Pricing:**
- Free during the preview.
- Announced prices after that: $0.30 per billion rows read, $0.30 per million rows written, and $0.10 per GB-month of storage per region.
- An idle database is moved into object storage, so it costs almost nothing while unused.

**Limits (preview):** 50 databases per account and 1 GB per database. Both can be raised through support.

**Access:**
- Over HTTP: the libSQL remote protocol, with an SQL API.
- SDKs for TypeScript, Go, Rust and .NET. The Go SDK is `github.com/tursodatabase/libsql-client-go/libsql` through `database/sql`.
- `@bunny.net/database-client` for Edge Scripting, Bun and Node.
- A shell, and a CLI (`bunny db`) that also runs migrations.

**Architecture and durability:**
- **Writes:**
  - There is one active primary, which takes all writes; replicas forward writes to it.
  - Several primary regions can be configured, but only one is active at a time, and it is chosen by latency. The primary can move after the database wakes from idle.
- **Storage:** data at rest lives in Toronto or Frankfurt. Compute runs separately in whichever regions are active.
- **Commit and data loss:**
  - A write counts as committed once it is in the primary's write-ahead log.
  - The log is uploaded to storage every 10 s or every 4096 page changes, whichever comes first. **Up to 10 s of writes can be lost in a failover.**
- **Snapshots:** hourly while the database is online, and a full snapshot when it goes idle. A restore loads the latest snapshot and replays the log after it.
- **Failover:** automatic. If both storage and the primary fail at once, the data is unavailable until storage recovers.
- **Idle behavior:** a database that gets no requests is moved to object storage and removed from all regions. The first request afterwards waits for it to come back. The docs don't say how long that takes.

**Consistency:**
- Transactions are serializable. Statements inside `BEGIN` run on the primary.
- A connection always sees its own writes.
- Reads from a replica never go backwards in time.
- **Not guaranteed:** a read on a different connection or instance seeing a write that just committed. Replication is asynchronous.
- To force a read to include a specific earlier write, pass that write's `replication_index` with the read. Not every SDK exposes this.

**Fit with meetspace's planned schema** (written for SQLite's dialect instead of Postgres):
- **Available as planned:** partial unique indexes (one owner per group), CHECK constraints, generated columns, window functions, `FILTER` on aggregates, `ON CONFLICT` upserts.
- **Needs a different approach:**
  - `tsvector` becomes FTS5 (availability on Bunny Database is unverified).
  - JSONB becomes JSON stored as text.
  - `date_trunc` becomes `strftime`.
  - `TIMESTAMPTZ` becomes text or an integer.
  - The haversine distance is computed in application code, unless SQLite's math functions are enabled (unverified).
- **Job queue:** River's SQLite driver (`riversqlite`) is an **early preview**, "needs more vetting to be considered fully production ready", and needs a single-connection pool. River has tested it against libSQL, but not against a remote, replicated database. A hand-written outbox table plus poller is the safer choice.

### Bunny Storage (object storage)

| Tier | Price | Minimum |
|---|---|---|
| Standard (HDD) | $0.01/GB-month for the first and second regions, $0.005/GB-month for each further region (up to 9) | $1/month |
| Edge (SSD) | $0.02/GB-month per region (up to 15) | $1/month |

- There are no API request fees, API egress is free, and traffic from storage to the Bunny CDN is free.
- **S3-compatible API: public preview.**
  - Endpoint: `https://{region}-s3.storage.bunnycdn.com`.
  - Credentials: the access key ID is the storage zone name, which is also the bucket name. Read-only keys are supported.
  - Lifecycle policies, versioning and fine-grained access controls are limited or missing.
- **Other access:** SFTP and a native HTTP API.
- **Versus DO Spaces ($5 flat for 250 GB):** at meetspace's expected size this costs the $1 minimum.

### Edge Scripting (edge functions)

JavaScript and TypeScript on Deno and V8, running at bunny.net's edge locations. **Go is not supported.** Compiling Go to WebAssembly isn't documented and would be up against the 10 MB script and 500 ms startup limits.

**Script types:**
- **Standalone:** the script *is* the origin for a pull zone. By default it only runs on a cache miss, so cached pages never invoke it.
- **Middleware:** sits in front of an existing origin, with hooks on the request to the origin and on its response.
  - With **"run before cache"** turned on, the script sees every request, including cache hits.
  - The client-side hooks (`onClientRequest` and `onClientResponse`) that come with it are marked **preview**.

**Pricing:** $0.02 per 1,000 s of CPU time plus $0.20 per million requests.
- Both are billed in whole increments, so any usage costs at least $0.22 a month.
- CPU time excludes time spent waiting on the network, such as `fetch` calls or database queries.
- CDN bandwidth is billed separately.

**Limits:** per request, 30 s of CPU and 50 outbound requests; 128 MB of memory, a 10 MB script and 500 ms to start up; 128 environment variables of up to 2 KB each. A script that keeps going over a limit is throttled or terminated.

**Runtime:**
- **`waitUntil`** keeps the script running after the response is sent. It is tied to that request and is not durable.
- **APIs:** a Cache API, HTMLRewriter, `node:fs`, `node:tls`, WebSockets and server-sent events.
- **Config:** environment variables and secrets.
- **Deploys:** GitHub integration and GitHub Actions deploys, plus deploy history with rollback.
- **Observability:** logs and statistics.
- **Database:** connects to Bunny Database through environment variables.

**Not found in the docs:** scheduled or cron triggers (the full docs were searched on 2026-09-29), and any queue or pub/sub.

### CDN (already in the plan)

These features are unchanged from the current plan: pull zones, custom hostnames with free Let's Encrypt SSL, the purge API, edge rules, origin shield, request coalescing and perma-cache.

For multi-host serving, two pull-zone settings matter:
- **Forward Host Header** passes the visitor's hostname to the origin.
- The **Requested Hostname** cache-key option caches each hostname separately.

The PRD's limit of 500 custom hostnames per standard account was not re-checked for this document.

### Not offered by bunny.net

- **Email:** the docs' email examples use Resend or SendGrid from an Edge Script. Email is evaluated separately in `email.md`.
- **Queue or pub/sub.**
- **Cron or scheduled triggers.**
- **Managed Postgres.**

---

## Deployment Shapes for meetspace

### A. Container + self-hosted Postgres (keeps Go and the current design)

```
Visitor ──▶ Bunny pull zone (custom hostnames, free SSL, cache)
              └─ origin: Magic Container app (1 region, 1 instance)
                   ├─ container: Go app (River jobs, tickers)
                   └─ container: postgres:17-alpine ── volume (10 GB)
                                   └─ scheduled pg_dump / WAL archive ──▶ Bunny Storage
Uploads ──▶ Bunny Storage (S3 API, preview) ──▶ served through the CDN
```

- **What stays as planned:** Go, Postgres, the planned schema, River as the durable queue, and in-process tickers.
- **What goes away:** the droplet, Caddy, origin TLS, OS patching and the firewall.
- **What's new:** backups become essential. Restoring from them is the only recovery from a disk failure.

### B. All-edge (TypeScript)

```
Visitor ──▶ Bunny pull zone ──▶ Standalone Edge Script (TS) ──▶ Bunny Database (libSQL)
External timer (every 5 min) ──▶ POST /tasks/tick (secret header) ──▶ same script
```

- **Different plan:** the app is written in TypeScript and the schema in SQLite's dialect. Nothing has been built yet, so this changes the development plan, not existing code.
- **Durable email:** write an outbox row in the same transaction as the change, send right away with `waitUntil`, and have the timer retry anything still unsent.
- **Maturity:** this shape relies on two preview products (Bunny Database, and middleware hooks if they are used).

### C. Hybrid (container plus edge middleware)

This is A with a middleware script in front, used for:
- rejecting unregistered hostnames before they reach the origin;
- rate limiting at the edge;
- adding personal fragments to cached pages with HTMLRewriter.

It doesn't save money. It's an option to add later.

---

## Scheduler (for edge functions)

A container runs continuously, so meetspace's timed work runs as goroutines inside it. That work is reminders every 5 minutes, hourly cleanup, outbox retries and nightly backups. An edge script only runs when a request arrives, so something outside has to call a protected endpoint on a timer.

| Option | Cost | Caveat |
|---|---|---|
| cron-job.org or similar | free | Depends on a small third-party service |
| GitHub Actions schedule | free | Minimum 5 min; runs are often 10–30 min late and occasionally skipped |
| Uptime monitor (UptimeRobot, Better Stack) | free tier | Doubles as monitoring; minimum intervals vary by plan |
| Tiny Magic Container running a sleep-and-curl loop | ~$1–2/month | Keeps everything on bunny.net |

meetspace's timing is forgiving: a reminder sent 20 minutes late is fine. The tick endpoint must be safe to call twice. `reminder_sent_at` and deleting expired rows already make it so.

---

## Cost Estimates (monthly)

Assumptions:
- Launch-scale traffic.
- Container memory of about 1 GB in total: the Go app (~100–150 MB) plus Postgres (~300–500 MB) plus overhead.
- Mostly idle CPU, averaging 5–10% of one core.
- About 1M uncached script requests a month for the edge shape: every page view triggers 3–4 fragment requests.

| | Droplet plan (PRD §6) | A. Container + Postgres | B. All-edge |
|---|---|---|---|
| Compute | $12 (2 GB droplet) | ~$3.65 RAM + ~$0.75–1.50 CPU | ~$0.22–1 |
| Database | included | $1 (10 GB volume) | $0 in preview; cents after |
| Object storage | $5 (Spaces) | $1 (Storage minimum) | $1 |
| CDN | ~$1 | ~$1 | ~$1 |
| Scheduler | included | included | $0–2 |
| **Total, excluding email** | **~$18** | **~$7–8** | **~$2–5** |

Email is deliberately left out of the totals. It is a separate service that any of these shapes can call over HTTPS, and it was the largest line in the earlier totals (Resend at $20 made them ~$38, ~$27 and ~$23–25). See `email.md` for the provider comparison.

---

## Risks

1. **Data durability (A):** a volume is one disk with no replication. Recovery time and data loss depend entirely on our backup frequency: nightly dumps mean losing up to a day, WAL archiving means losing minutes.
2. **Availability (A):** node maintenance takes the database down until the node returns, and the database can't move to another machine without starting empty.
3. **Preview products (B):** Bunny Database, the S3 API for Storage, and the before-cache middleware hooks are all in preview. Pricing, limits and APIs may change.
4. **Lock-in (B):** a libSQL schema and bunny.net-specific runtime APIs (`Bunny.v1.waitUntil`, `servePullZone`) are harder to move than a Postgres container.
5. **Cost visibility:** Magic Containers offers no per-item cost breakdown.

## Open Questions (verify before choosing)

1. With a container origin, does **Forward Host Header** give the Go app the visitor's hostname? This replaces the Phase 9 spike and needs no Caddy.
2. Does deploying a new app image in a two-container pod also restart Postgres?
3. How does Postgres perform under gVisor with the 10 MB/s volume limit? Load test before committing.
4. Is volume billing active? One search result said billing hadn't started yet; the current docs list $0.10/GB-month.
5. Does Bunny Database's libSQL build include FTS5 and SQLite's math functions?
6. How long is Bunny Database's cold start after idle, and when do GA pricing and limits arrive?
7. When will the Storage S3 API reach GA, and does it support presigned URLs?
8. Is there private networking between separate Magic Container apps?
9. What is the current custom-hostname limit per pull zone (the PRD assumes 500)?

## Criteria for Comparing Other Options

Record the same facts for each alternative:

- Monthly cost at launch scale (compute, database, storage, CDN; email and SMS are separate services, see `email.md`).
- Can it run the Go binary unchanged? Does it offer Postgres?
- Database durability: replication, automatic backups, worst-case data loss.
- Durable jobs and scheduled work: a built-in cron, or do we bring our own?
- Custom domains with free SSL at hundreds of hostnames, and whether the app sees the visitor's hostname.
- Object storage that works with S3 tools, and its price.
- Maturity (GA versus preview) and lock-in.
- Ongoing operations: OS patching, TLS, monitoring, deploys.

---

## Sources

- Magic Containers: [overview](https://bunny.net/magic-containers/), [pricing](https://bunny.net/docs/magic-containers/pricing), [limits](https://bunny.net/docs/magic-containers/limits), [endpoints](https://bunny.net/docs/magic-containers/endpoints), [regions](https://bunny.net/docs/magic-containers/regions), [autoscaling](https://bunny.net/docs/magic-containers/autoscaling), [FAQs](https://bunny.net/docs/magic-containers/faqs), [PostgreSQL guide](https://bunny.net/docs/magic-containers/guides/postgresql)
- Persistent volumes: [docs](https://bunny.net/docs/magic-containers/persistent-volumes), [announcement (2026-03-04)](https://bunny.net/blog/magic-containers-now-supports-persistent-volumes-storage-that-survives-restarts-and-redeployments/)
- Bunny Database: [docs](https://bunny.net/docs/database), [announcement](https://bunny.net/blog/meet-bunny-database-the-sql-service-that-just-works/), [durability and consistency](https://bunny.net/docs/database/durability-and-consistency), [replication](https://bunny.net/docs/database/replication), [limits](https://bunny.net/docs/database/limits), [Go client](https://bunny.net/docs/database/connect/go)
- Bunny Storage: [S3 API](https://docs.bunny.net/storage/s3), [pricing](https://bunny.net/pricing/storage/)
- Edge Scripting: [overview](https://bunny.net/docs/scripting/index), [pricing](https://bunny.net/docs/scripting/pricing), [limits](https://bunny.net/docs/scripting/limits), [runtime](https://bunny.net/docs/scripting/runtime), [before-cache execution](https://bunny.net/docs/scripting/before-cache), [middleware](https://bunny.net/docs/scripting/middleware/overview)
- River: [SQLite support](https://riverqueue.com/docs/sqlite), [announcement](https://riverqueue.com/blog/sqlite-and-pro-dbsql-durable-periodic-jobs-performance-boosts)
- Docs index used for searching: [bunny.net/docs/llms.txt](https://bunny.net/docs/llms.txt)
