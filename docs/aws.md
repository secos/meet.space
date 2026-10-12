# Hosting Option: AWS (Go version and functions-only)

Could meetspace run on AWS instead of a DigitalOcean droplet? This file covers two shapes: **keeping the Go binary** on always-on compute with Postgres, and a **functions-only** approach built on Lambda. It collects what AWS offers, what each product costs, how each maps to the PRD, and what is still unverified. It follows the structure of `bunny.md` and `cloudflare.md`, but does not compare across vendors (that comes later). Email is evaluated as a separate service in `email.md`, so the cost totals here exclude it.

**Researched:** 2026-09-29, from AWS docs and pricing pages. Compute and RDS unit prices come from the AWS Price List bulk files for us-east-1 (the EC2 file is dated 2026-09-25); monthly figures use 730 hours and are our arithmetic. Everything else was read on docs.aws.amazon.com or aws.amazon.com pages through a summarizing fetch tool, so numbers are "per docs summary". Items marked **[unverified]** came only from third-party pages, search snippets, memory, or inference. Prices and statuses change often.

**Companion documents:** `prd.md` (requirements, §6 architecture and cost estimate), `development-plan.md` (current droplet-based plan), `bunny.md`, `cloudflare.md`, `email.md` (email as a separate service).

---

## Summary

| Question | Go version | Functions-only |
|---|---|---|
| Can the Go app run? | Yes, unchanged: on EC2, Lightsail, ECS Fargate or Lightsail containers. | Yes: an arm64 `provided.al2023` binary, or the unchanged `net/http` server through Lambda Web Adapter. `go1.x` is deprecated. |
| Postgres? | Full Postgres: RDS, Lightsail Database, Aurora, or self-hosted on the same box. | Not full Postgres. Aurora DSQL is pay-per-use but has gaps (no `tsvector`, triggers, PL/pgSQL). Aurora Serverless v2 is real Postgres but costs about $44/month minimum and never auto-pauses here. DynamoDB is a full rewrite. |
| Durable jobs and cron? | River on Postgres and in-process tickers work as planned. | River is not a fit. Use a database outbox, EventBridge Scheduler (free at this volume) and SQS. |
| Custom domains for paid groups? | CloudFront SaaS Manager in front of the origin. | The same. A Lambda Function URL cannot take a custom domain itself, so CloudFront is required. |
| Email? | Separate service (see `email.md`). Amazon SES is one option and comes out cheapest there, at about $5 for 50k emails a month. | Same. |
| Uploads? | S3 with presigned URLs, served through CloudFront. | Same. |
| Monthly cost (excluding email) | ~$14 (everything on one Lightsail box) to ~$67 (Fargate + ALB + RDS), ~$43 for the "recommended" EC2 + RDS. | ~$3–10 (DynamoDB or DSQL), before a $20/month tenant fee if custom domains exceed 10. Aurora Serverless v2 variants start around $45. |
| Biggest catch | AWS is not cheaper than the droplet for the Go shape. A managed database is what costs. App Runner is closed to new customers. | The database. DSQL forces schema and search rework, and River cannot run on it. |

S3 is cheap and R2 is cheaper on egress. The largest saving on AWS is not in this file: email is a separate service, and SES at about $5 for 50k emails a month versus $20 for Resend is the biggest single line difference (see `email.md`).

---

## Shared layer (both shapes)

### CloudFront and SaaS Manager (custom domains)

**Pricing models.** CloudFront now has two, and they don't mix for SaaS:

| Model | Details |
|---|---|
| Pay-as-you-go | Always-free tier of 1 TB data transfer out and 10M requests per month, renewing indefinitely, plus 2M CloudFront Function invocations. After that, US/EU transfer is $0.085/GB (down to $0.02 at very high volume) and HTTPS requests are about $0.01 per 10,000. Origin-to-CloudFront transfer from AWS origins is free. Shield Standard is included. WAF is billed separately (not fetched). |
| Flat-rate plans (GA November 2025) | Free $0 (1M requests, 100 GB), Pro $15 (10M requests, 50 TB), Business $200, Premium $1,000. No overage charges. Includes WAF and DNS. **Multi-tenant distributions (SaaS Manager) are unsupported on flat-rate plans**, so SaaS use means pay-as-you-go. |

**SaaS Manager** (multi-tenant distributions and "distribution tenants"). Status: GA since 2025-04-28.

| Item | Fact |
|---|---|
| Tenant pricing | First 10 tenants free, tenants 11–200 cost "$20/month subscription", tenants over 200 cost $0.10 per tenant per month. The page is ambiguous about whether the $20 is flat or per tier. Example: 1,000 tenants is about $100/month plus traffic (derived). |
| Quotas | 10,000 tenants per account (adjustable), 20 multi-tenant distributions, 100 connection groups, 100 aliases per tenant. |
| Certificates | Free CloudFront-managed ACM certificates per tenant, HTTP-validated, auto-renewed, usable only with CloudFront. Certificates live in us-east-1. |
| Customer DNS | The customer creates a CNAME to the connection group's endpoint. The docs suggest a cleaner pattern: we own `customer-123.ourdomain.com`, hold the validation TXT record in our own zone, and the customer only points `www.customer.com` at that name. |
| Apex domains | A plain CNAME can't sit at an apex. AWS recommends a Route 53 ALIAS/A record, which most DNS hosts lack. Same limitation as the other vendors. |
| Origins | S3 (with OAC), ALB, EC2, Lambda Function URL, API Gateway and private VPC origins. Origin domains can be parameterized per tenant. |
| Visitor hostname at the origin | ALB-style origins can use the `AllViewer` origin request policy, which forwards the customer's `Host`. For Lambda Function URL or API Gateway origins the `Host` must be the origin's own domain, so the tenant hostname has to be copied into a custom header (for example with a CloudFront Function). **[unverified]**: the preset behavior came from a search snippet and the header trick from third-party posts. |
| Tenant customization | Per-tenant WAF web ACL, geo restrictions, certificate and parameters. |
| Not supported in multi-tenant | Continuous deployment, OAI (use OAC), TTL settings (use cache policies), PriceClass. |

**Invalidation.**
- The first 1,000 paths or tags per month are free, then $0.005 each. A wildcard counts as one path.
- **Cache-tag invalidation** (added 2026-04-29): the origin returns a tag header (name is configurable), and we invalidate `#tagname`. Up to 50 tags per object, 256 characters per tag, and P95 propagation under 5 seconds.
- Limits: 150 paths or tags per second, and 1 wildcard invalidation per second.
- **Invalidation is per tenant** (`CreateInvalidationForDistributionTenant`). Purging one changed page on the main domain and the group's custom domain is two calls. There is no documented single call that purges every tenant. That matches the PRD's "purge every hostname the page is served from" rule, but it is one call per hostname.

**Edge compute.** CloudFront Functions are $0.10 per 1M invocations after 2M free (10 KB, 2 MB memory). Lambda@Edge is $0.60 per 1M requests plus duration and is never in flat-rate plans.

### S3 (uploads)

- PUT, COPY, POST and LIST are $0.005 per 1,000. GET is $0.0004 per 1,000. Storage is about $0.023/GB-month **[unverified, from memory]**. Transfer from S3 to CloudFront is free.
- Presigned URLs (GET and PUT), lifecycle rules and OAC behind CloudFront are standard. Presigned POST policies can enforce `content-length-range` for the 10 MB upload limit **[unverified, general knowledge]**.
- At about 20 GB, uploads cost roughly $0.50 a month.

### SES (email)

Email is evaluated as a separate service in `email.md`, which compares 14 providers, so the SES facts are kept there and not repeated here. In short: SES is the cheapest clean option ($0.10 per 1,000 on the "a la carte" plan, so about $5 at 50k emails a month), but new accounts default to a $0.16 per 1,000 plan and must be switched, the SES-specific free tier closed to new customers on 2026-07-21, sandbox exit is a manual review, requests need SigV4 signing, there is no idempotency key, and enforcement is account-wide. SES tenants (per-group reputation isolation) cost $0.005 per tenant per month plus $0.005 per 1,000 emails on a la carte; an earlier version of this file said 1,000 tenants were included, which is only true on the Enterprise plan.

Outbound port 25 is throttled on EC2 and Lightsail (a removal form takes up to 48 hours per region). Ports 587 and 465 are unrestricted, and every email provider here is called over HTTPS, so this doesn't affect us.

### Route 53, ACM, API Gateway custom domains

- Route 53: $0.50 per hosted zone per month, queries $0.40 per million, alias queries to CloudFront free. We only need our own zone.
- ACM public certificates for CloudFront, ALB and API Gateway are free.
- **API Gateway custom domains scale poorly per tenant:** `CreateDomainName` and `DeleteDomainName` are limited to one call per 30 seconds per account (not adjustable), roughly 120 an hour. The default quota is 120 custom domains per region. CloudFront tenants are the better fit.

### Free-tier and credit rules

- Accounts created on or after 2025-07-15 get a **credit model**: $100 at sign-up plus up to $100 more for using foundational services, up to $200. The free plan ends at 6 months or when credits run out, and then the account closes with data kept for 90 days unless upgraded. One source says the credits last 6 months and another says 12; **the sources disagree**. Joining an AWS Organization expires the credits immediately.
- The CloudFront always-free tier (1 TB and 10M requests) and the Lambda free tier (1M requests, 400,000 GB-s) are permanent.
- Older accounts keep the legacy structure (12-month trials plus always-free). Which S3 and SES allowances survive is unconfirmed.
- Cost estimates below are **list price, no credits**.

### Unpleasant surprises

| Item | Price |
|---|---|
| NAT gateway | $0.045/hour = **$32.85/month** plus $0.045/GB processed |
| Public IPv4 address | $0.005/hour = **$3.65/month** each, in use or idle. Applies to Fargate task IPs and each ALB Availability Zone. |
| Data transfer out | 100 GB/month free (aggregated across services), then $0.09/GB. With CloudFront in front, origin-to-CloudFront traffic from AWS origins is free. |
| CloudWatch Logs | $0.50/GB ingested, about $0.50–2 a month for one small app |
| Lightsail transfer overage | $0.09/GB above the plan allowance |

---

## Shape 1: Go version (keep the binary)

### Compute

| Option | Unit price | Monthly | Notes |
|---|---|---|---|
| **Lightsail instance** | $5 (0.5 GB), $7 (1 GB), **$12 (2 GB, 2 vCPU, 60 GB SSD)**, $24 (4 GB) | $12 for 2 GB | Public IPv4 bundled. Transfer allowance included (3 TB per a third-party page). Snapshots $0.05/GB-month. |
| **Lightsail container service** | Micro (0.25 vCPU, 1 GB) **$10**, Small (0.5 vCPU, 1 GB) **$15** | $10–15 | Includes an HTTPS endpoint with a managed certificate and a transfer allowance. No ALB needed. |
| **EC2 t4g.small** (2 GiB) | $0.0168/hour | **$12.26** (+ $3.65 public IPv4, + EBS $0.08/GB-month) | 1 GiB (t4g.micro, $6.13) is tight for the app and Postgres. |
| **Fargate ARM** (0.5 vCPU, 1 GB) | $0.03238/vCPU-hour, $0.00356/GB-hour | **$14.42** ($8.51 for 0.25 vCPU) | Needs an ALB (about $25/month realistic) or CloudFront in front, because task IPs change on every deploy. |
| Fargate Spot | "up to 70%" off | about $4–5 **[unverified]** | Not suitable for a single replica: it stays down until capacity returns, with a 2-minute warning. |
| **App Runner** | — | — | **Closed to new customers as of 2026-04-30** (maintenance mode). AWS points to ECS Express Mode. Do not use. |
| **ECS Express Mode** | no extra charge | Fargate + ALB + logs | GA. One API call creates a Fargate service, an ALB with TLS, autoscaling and a URL. |

**HTTP front door.** An ALB is $0.0225/hour = $16.43/month, plus $0.008 per LCU-hour and $0.005/hour per public address per AZ, about **$25/month** all in (ALB IP charging **[unverified]**). A single EC2 or Lightsail box avoids this by terminating TLS in Caddy or in Go itself. CloudFront in front of the box adds edge caching and the custom-domain layer.

### Postgres

| Option | Price | HA and durability | Fit |
|---|---|---|---|
| **RDS PostgreSQL** db.t4g.micro (1 GiB) | $0.016/hour = **$11.68** Single-AZ, $23.36 Multi-AZ | Backups free up to provisioned size, PITR 1–35 days (default 7). Multi-AZ has a synchronous standby, failover about 60–120 s. **[unverified this session]** | Full Postgres. River, LISTEN/NOTIFY and `SKIP LOCKED` all work. 1 GiB RAM. |
| RDS db.t4g.small (2 GiB) | $0.032/hour = **$23.36** Single-AZ, $47.45 Multi-AZ | same | The realistic size. |
| RDS storage | gp3 $0.115/GB-month Single-AZ, $0.23 Multi-AZ, 20 GB minimum | — | 20 GB is $2.30. |
| **Lightsail managed database** | 1 GB **$15**, 2 GB $30, 4 GB $60. HA doubles the price. | Automatic backups and 5-minute-granularity PITR for 7 days, free. HA has a synchronous standby. | Full Postgres, simple. Limited AWS integration. |
| **Aurora Serverless v2** | $0.12/ACU-hour, 0.5 ACU minimum, storage $0.10/GB-month, I/O $0.20 per million | 6-way storage replication, continuous backup | Full Postgres. Auto-pause (GA November 2024) is blocked by any open connection or an RDS Proxy. River, a connection pool and a 5-minute ticker mean it never pauses, so it costs **$43.80/month minimum** plus storage and I/O. |
| **Aurora DSQL** | $8 per million DPU, $0.33/GB-month | 3-AZ synchronous, no lag loss in region | Not viable for the Go shape (see the functions-only section). |
| **Self-hosted on the same box** | Included in the instance | EBS snapshots ($0.05/GB-month). RPO is the snapshot interval unless we archive WAL. | Cheapest. Same trade-off the droplet has. |

### Networking notes

- Run tasks in a public subnet or use an instance with a public IP, to avoid a NAT gateway.
- Sending through an email provider's HTTPS API or SMTP 587 avoids the port 25 restriction.

### Deployment shapes

**1A. One Lightsail box** (Go, Postgres and Caddy together) with CloudFront in front for custom domains, S3 for uploads, and an external email provider (see `email.md`).
- Closest to the current droplet plan. The database is on the same disk, with snapshots as backup, and there is no HA.

**1B. EC2 t4g.small + RDS t4g.small (recommended AWS-managed shape)**
```
Visitor ──▶ CloudFront (SaaS Manager tenants, free ACM certs, cache)
              └─ origin: EC2 t4g.small (Go app, Caddy or Go TLS)
                           └─ RDS PostgreSQL t4g.small ── PITR + free backups
Uploads ──▶ S3 (presigned URLs) ──▶ CloudFront (OAC)
Email ──▶ separate service (see email.md)
```
- Managed database with real backups and PITR, everything else as planned. River works.

**1C. Lightsail containers + Lightsail Database.** Both managed, no ALB, about $22–45.

**1D. Fargate + ALB + RDS.** No servers to patch, but about $25 for the ALB alone, so the most expensive of the four.

**HA-ish variants:** RDS Multi-AZ adds about $28 on 1B. Two Fargate tasks run the in-process tickers twice, so reminders and cleanup have to be idempotent (they already are via `reminder_sent_at` and row deletion), or River's leader election has to be used.

---

## Shape 2: Functions-only (Lambda)

### Lambda

**Pricing** (us-east-1):

| Item | Price |
|---|---|
| Requests | $0.20 per million |
| Duration, arm64 | $0.0000133334 per GB-s (x86: $0.0000166667). arm64 tiers are third-party **[unverified]** |
| Free tier | 1M requests and 400,000 GB-s per month, permanent |
| Provisioned concurrency | $0.0000041667 per GB-s (x86). Go can't use SnapStart. |
| Response streaming | First 6 MB per request free, then $0.008/GB |

**Limits:** 128 MB–10,240 MB memory, 15-minute timeout, 1,000 concurrent executions by default (new accounts start lower), 6 MB request and response for synchronous invocations, 250 MB unzipped package or 10 GB container image.

**Go.**
- `go1.x` is deprecated (function updates blocked from 2027-03-03). `provided.al2023` is supported until 2029-06-30. `provided.al2` was deprecated on 2026-07-31.
- Build: `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags lambda.norpc -o bootstrap`.
- **Lambda Web Adapter** (v1.1.0) runs an unchanged HTTP server: as a layer for zip packages or one Dockerfile line for containers. It supports Function URL, API Gateway and ALB triggers, and buffered or streaming responses. Go `net/http` isn't named, but nothing prevents it. The process must start listening quickly.
- Cold start: blogs report tens of milliseconds for Go on arm64. **[unverified]**: no AWS figure. Measure with our own binary.
- Newer features (not a fit at launch scale): Lambda Managed Instances (EC2-backed, never scales to zero, runtime guides for Java, Node and Python) and durable functions (SDKs for JS/TS, Python and Java, **not Go**).

**HTTP front doors:**

| Option | Price per 1M requests | Custom domain | Visitor Host at the function |
|---|---|---|---|
| **Function URL** | $0 (Lambda charges only) | **None natively.** Needs CloudFront in front. | Payload shows the lambda-url domain. Behind CloudFront the viewer `Host` isn't forwarded, so copy it to a header with a CloudFront Function and use `AllViewerExceptHostHeader` **[unverified, third-party posts]**. |
| API Gateway HTTP API | $1.00 (first 300M) | Yes, 120 domains per region by default, 1 domain change per 30 s | Original Host arrives in `headers.host` **[unverified]** |
| API Gateway REST API | $3.50 | Yes | same |
| ALB | $0.0225/hour + LCUs | Yes | Passes through |

For per-group domains, **CloudFront (SaaS Manager) in front of a Function URL** is the natural route. Function URLs don't have WAF or PrivateLink of their own.

### Databases usable from Lambda

**Aurora DSQL** is the only true pay-per-use Postgres-wire option. Status: GA since 2025-05-27, 99.99% single-region availability design.

| Item | Fact |
|---|---|
| Price | $8 per million DPU, $0.33/GB-month. Free tier 100,000 DPU and 1 GB per month. No idle cost. Pricing page examples are for US East (Ohio); us-east-1 is assumed the same **[unverified]**. Query DPU costs (about 0.06 for a small read, 0.04 for a small insert) are from a third-party summary. |
| Access | IAM-token auth over a public endpoint, so Lambda needs no VPC. Connection cap: 100 new connections per second (not adjustable), 10,000 total. |
| Durability | Synchronous 3-AZ commit, so an in-region failure loses nothing. Backups are AWS Backup full-cluster backups and a restore creates a new cluster. **Point-in-time restore is not documented.** |

Compatibility with meetspace's schema:

| Feature | DSQL |
|---|---|
| Partial and unique partial indexes | **Supported** (`CREATE UNIQUE INDEX ASYNC ... WHERE`, one owner per group works) |
| CHECK, foreign keys, sequences, generated columns | Supported |
| `TIMESTAMPTZ` | Supported (UTC only) |
| `jsonb` | Supported but **not indexable** (no GIN), 1 MiB limit |
| `tsvector` / `tsquery` | **Not in the supported-types list**, so treat search as unsupported. Needs external search or a `LIKE` workaround. |
| Triggers, PL/pgSQL, `CREATE TYPE`, temp tables, `TRUNCATE` | **Not supported** (SQL functions only) |
| `SELECT FOR UPDATE` | Supported as optimistic conflict detection, not locking. `SKIP LOCKED`, advisory locks and LISTEN/NOTIFY are not listed **[unverified, inferred]**. |
| Isolation | Fixed at Repeatable Read. Conflicts return SQLSTATE `40001` (`OC000`/`OC001`), so every write path needs retry logic. |
| Limits | 3,000 rows mutated and 10 MiB per transaction, 5-minute transactions, 1-hour connections, 1 database per cluster, DDL and DML in separate transactions. |

**Aurora Serverless v2** is full Postgres, but from Lambda it needs a VPC, and the pricing above (about $44/month minimum) applies because the reminder cron keeps it awake. RDS Proxy would add about $5.48/month at 0.5 ACU ($87.60 if an 8-ACU minimum applies; the sources conflict **[unverified]**) and also blocks auto-pause. A VPC-bound Lambda that also calls SES, SQS or Secrets Manager needs VPC endpoints or a NAT gateway (about $32.85/month). The Data API (first 1M requests free, then $0.35/M) avoids the VPC but adds latency and has unresearched Postgres-feature limits.

**DynamoDB on-demand** (rewrite option): $0.625 per million writes, $0.125 per million reads, $0.25/GB-month with 25 GB free, PITR $0.20/GB-month. Cheapest at this scale (about $2/month), but joins, partial unique indexes, CHECK constraints and full-text search all need redesign (search would need OpenSearch or an in-app index). TTL deletes are free and Streams give a natural outbox.

**Not researched:** RDS provisioned pricing behind RDS Proxy for Lambda, Neon and Supabase through AWS Marketplace.

### Scheduling, queues, and the job pattern

- **EventBridge Scheduler:** $1.00 per million invocations after 14M free per month. Reminders every 5 minutes (about 8,640 a month) plus hourly cleanup (720) cost $0. At-least-once delivery, configurable retries. One-minute cron resolution is believed but not confirmed **[unverified]**.
- **SQS:** standard $0.40 per million requests, 1M free a month **[prices third-party or from memory]**, at-least-once, DLQ via redrive policy, and Lambda polls it through an event source mapping.
- **River:** **[unverified, inferred]** not viable on DSQL, which lacks `SKIP LOCKED`, advisory locks, LISTEN/NOTIFY, `CREATE TYPE` and PL/pgSQL, all of which River relies on. On real Postgres River works, but needs a long-lived worker, which defeats the functions-only goal. Test by running River's migrations against a DSQL cluster before ruling it out.
- **Recommended pattern instead:**
  1. Insert an `email_outbox` row in the same transaction as the business write.
  2. An EventBridge Scheduler tick (every minute or so) runs a Lambda that claims due rows, enqueues them to SQS, and a worker Lambda calls the email provider. SES v2 has no idempotency key, so the outbox row must dedupe retries. Attempts are counted and a row is marked dead after N tries.
  3. DSQL has no `SKIP LOCKED`, so run a single claimer or partition by `hash(id) % N`, and keep each claim under the 3,000-row transaction limit.
  4. Reminders are one Scheduler rule every 5 minutes that finds due meetings and enqueues them. `reminder_sent_at` keeps it idempotent.
  5. For a DynamoDB variant the write stream triggers the sender Lambda directly.

### Deployment shapes

**2A. Lambda (Go) + CloudFront + Aurora DSQL**
```
Visitor ──▶ CloudFront (SaaS Manager, tenant hostname header via CloudFront Function)
              └─ Lambda Function URL ──▶ Go (Web Adapter or native handler) ──▶ Aurora DSQL
EventBridge Scheduler (every 5 min / hourly) ──▶ tick Lambda ──▶ SQS ──▶ worker Lambda ──▶ email provider
Uploads ──▶ S3 (presigned URLs, OAC via CloudFront)
```
- Keeps Go and most of the schema, but search, triggers and River all change. Every write needs serialization-retry handling.

**2B. Lambda + Aurora Serverless v2.** Real Postgres, but $44+ for the database and likely a NAT or VPC endpoints. Not worth it at this scale.

**2C. Lambda (TypeScript or Go) + DynamoDB.** The cheapest, at the cost of redesigning the whole data layer.

---

## Cost Estimates (monthly, list prices, no credits)

Assumptions:
- Launch-scale traffic: about 1M dynamic requests a month at 100 ms and 512 MB on arm64 (each page view triggers 3–4 fragment requests), 5–20 GB of uploads. Email and SMS are excluded (separate services: `email.md`, `sms.md`).
- Fewer than 10 custom-domain groups, so no SaaS Manager tenant fee. **Add $20/month once there are 11–200 groups**, then $0.10 per tenant beyond 200.
- CloudFront stays inside the free tier (1 TB and 10M requests).
- Logs about $1 a month. Data transfer stays inside the 100 GB allowance.
- The database lines are the shape's biggest variable.

| | Droplet plan (PRD §6) | 1A. One Lightsail box | 1B. EC2 + RDS | 1C. Lightsail + Lightsail DB | 1D. Fargate + ALB + RDS | 2A. Lambda + DSQL | 2C. Lambda + DynamoDB |
|---|---|---|---|---|---|---|---|
| Compute | $12 | $12 (2 GB) | $12.26 + $3.65 IPv4 | $12 (or $10–15 container) | $14.42 + ~$25 ALB | $0–0.90 | $0–0.90 |
| Database | included | included (snapshots ~$1) | RDS t4g.small $23.36 + $2.30 storage | Lightsail DB $15 | RDS t4g.small ~$25.66 | ~$2–8 (DPU + storage + backups) | ~$1–2 |
| Object storage | $5 | ~$0.50 | ~$0.50 | ~$0.50 | ~$0.50 | ~$0.50 | ~$0.50 |
| CDN and custom domains | ~$1 | $0–20 | $0–20 | $0–20 | $0–20 | $0–20 | $0–20 |
| Scheduler, queue | included | included | included | included | included | $0 | $0 |
| Logs | included | ~$1 | ~$1 | ~$1 | ~$1 | ~$1 | ~$1 |
| **Total, ≤10 custom domains, excluding email** | **~$18** | **~$14** | **~$43** | **~$29** | **~$67** | **~$4–10** | **~$3–5** |
| **Total, 11–200 custom domains, excluding email** | — | ~$34 | ~$63 | ~$49 | ~$87 | ~$24–30 | ~$23–25 |

Notes:
- The earlier totals included email (Resend at $20 for the droplet plan, SES at about $5 for the AWS shapes). Email is now a separate line in `email.md`.
- Aurora Serverless v2 variants (shape 2B) start at about $44 for the database alone and reach $130 or more with a NAT gateway and an 8-ACU proxy, so they are left out of the table.
- 1A has no managed database, no HA and no point-in-time restore beyond snapshots. The comparable managed-database shape is 1B or 1C.
- **HA-ish variants** (compute plus database only): 1B with RDS Multi-AZ is about $69, two Fargate tasks plus ALB plus Multi-AZ RDS is about $107, and Lightsail with the HA database is about $42.
- The DSQL and DynamoDB figures rest on unmeasured DPU and request-unit assumptions, and CloudWatch, S3 and transfer numbers partly come from memory. The free-tier credits for new accounts are excluded.

---

## Risks

1. **The managed database costs more than the compute (Go shape).** RDS or Lightsail Database is the single largest line, and AWS is more expensive than the droplet unless the database stays on the same box (1A).
2. **DSQL fit (functions-only).** No `tsvector`, triggers or PL/pgSQL, no documented point-in-time restore, an optimistic-concurrency retry burden on every write, a 3,000-row transaction cap, 100 connections per second, and River can't run. It is a schema and search rework, not a drop-in.
3. **Lambda cost cliffs.** Aurora Serverless v2 never pauses under this workload, and a VPC-bound Lambda needs NAT or endpoints. Both add $30–90 a month over the headline Lambda price.
4. **Custom-domain plumbing.** CloudFront SaaS Manager needs pay-as-you-go pricing, has a $20 fee past 10 tenants, and purges per tenant. Function URL and API Gateway origins need a workaround to see the customer's hostname.
5. **Apex domains** need customer-side ALIAS support, as elsewhere.
6. **Cost visibility and surprises.** NAT gateways, idle and in-use IPv4 addresses, and CloudWatch Logs are easy to add by accident.
7. **Account rules.** New accounts start on a credit model with a free plan that closes after 6 months (the sources disagree on the credit period). If SES is used for email, it also starts in a 200-emails-a-day sandbox until production access is approved (see `email.md`).
8. **Product churn.** App Runner was closed to new customers in April 2026, and several relevant features (cache-tag invalidation, DSQL) are under a year old.
9. **Lock-in.** Lambda, DSQL and CloudFront tenants are AWS-specific. The Go shape on RDS Postgres is the easiest to move.

## Open Questions (verify before choosing)

1. Behind CloudFront SaaS Manager, does the tenant hostname reach a Lambda Function URL, an EC2 origin, or an API Gateway origin, and how? Test each.
2. Is the $20 tenant subscription flat, or per tier of 11–200? Are managed certificates free?
3. Can one tag purge cover several tenants, or is it strictly one call per tenant?
4. (Email questions moved to `email.md`.)
5. Does a DSQL cluster accept River's migrations? Do LISTEN/NOTIFY, `SKIP LOCKED`, advisory locks and `tsvector` fail as inferred?
6. What does DSQL cost for meetspace's real queries (`EXPLAIN ANALYZE VERBOSE`), and is point-in-time restore available?
7. Go on Lambda: real cold-start and Web Adapter overhead with our binary.
8. Aurora Serverless v2: exact per-ACU, storage and I/O prices, and whether RDS Proxy has an 8-ACU minimum.
9. EventBridge Scheduler's minimum granularity (one minute is believed).
10. Fargate outbound port 25 behavior, Lightsail container transfer allowance, and CloudFront-to-Lightsail origin transfer pricing.
11. How long do new-account credits last (6 or 12 months)?
12. S3 storage price per GB, the permanent free allowances that remain for S3, and Spot Fargate pricing.

## Criteria for Comparing Other Options

The same criteria as `bunny.md`, answered for AWS only (no cross-vendor table yet):

| Criterion | Go version (1B: EC2 + RDS) | Functions-only (2A: Lambda + DSQL) |
|---|---|---|
| Monthly cost at launch scale (excluding email) | ~$43 (about $14 if Postgres shares the box) | ~$4–10 before tenant fees |
| Runs the Go binary unchanged? Postgres? | Yes, and full Postgres | Go yes (Web Adapter or handler); Postgres-wire only, with the gaps above |
| Database durability | RDS PITR 1–35 days, Multi-AZ optional (about +$28) | Synchronous 3-AZ commit; backups full-cluster only, PITR undocumented |
| Durable jobs and scheduled work | River and tickers as planned | EventBridge Scheduler + SQS + outbox table; no River |
| Custom domains with free SSL, hostname visibility | CloudFront SaaS Manager: 10 free, $20 for 11–200, then $0.10; Host forwarded to an EC2 or ALB origin | The same, but tenant hostname needs a header workaround for Function URL origins |
| S3-compatible storage and price | S3, GA, about $0.023/GB, presigned URLs | Same |
| Maturity and lock-in | Mostly GA and conventional; low lock-in | GA but young (DSQL 2025, cache tags 2026); high lock-in |
| Ongoing operations | OS patching on EC2 (none on Fargate or Lightsail containers), managed DB | No servers; retry logic, outbox and Scheduler wiring instead |
| Email | Separate service (`email.md`); SES is one option | Same |

---

## Sources

- Compute and pricing data: [AWS Price List bulk files, us-east-1](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/) (EC2, Fargate, RDS, ALB, NAT, IPv4), [Fargate pricing](https://aws.amazon.com/fargate/pricing/), [ECS pricing](https://aws.amazon.com/ecs/pricing/), [Lightsail pricing](https://aws.amazon.com/lightsail/pricing/), [App Runner availability change](https://docs.aws.amazon.com/apprunner/latest/dg/apprunner-availability-change.html), [ECS Express Mode](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/express-service-overview.html), [ELB pricing](https://aws.amazon.com/elasticloadbalancing/pricing/)
- Port 25: [EC2](https://repost.aws/knowledge-center/ec2-port-25-throttle), [Lightsail](https://repost.aws/knowledge-center/lightsail-port-25-throttle)
- RDS and Aurora: [RDS PostgreSQL pricing](https://aws.amazon.com/rds/postgresql/pricing/), [automated backups](https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/USER_WorkingWithAutomatedBackups.html), [Aurora pricing](https://aws.amazon.com/rds/aurora/pricing/), [Serverless v2 auto-pause](https://docs.aws.amazon.com/AmazonRDS/latest/AuroraUserGuide/aurora-serverless-v2-auto-pause.html), [RDS Proxy pricing](https://aws.amazon.com/rds/proxy/pricing/)
- Aurora DSQL: [pricing](https://aws.amazon.com/rds/aurora/dsql/pricing/), [FAQ](https://aws.amazon.com/rds/aurora/dsql/faqs/), [quotas](https://docs.aws.amazon.com/aurora-dsql/latest/userguide/CHAP_quotas.html), [migration guide](https://docs.aws.amazon.com/aurora-dsql/latest/userguide/working-with-postgresql-compatibility-migration-guide.html), [supported SQL features](https://docs.aws.amazon.com/aurora-dsql/latest/userguide/working-with-postgresql-compatibility-supported-sql-features.html), [supported data types](https://docs.aws.amazon.com/aurora-dsql/latest/userguide/working-with-postgresql-compatibility-supported-data-types.html), [index syntax](https://docs.aws.amazon.com/aurora-dsql/latest/userguide/create-index-syntax-support.html), [table syntax](https://docs.aws.amazon.com/aurora-dsql/latest/userguide/create-table-syntax-support.html), [foreign keys](https://docs.aws.amazon.com/aurora-dsql/latest/userguide/working-with-foreign-key-constraints.html), [concurrency control](https://docs.aws.amazon.com/aurora-dsql/latest/userguide/working-with-concurrency-control.html), [resiliency](https://docs.aws.amazon.com/aurora-dsql/latest/userguide/disaster-recovery-resiliency.html), [backup](https://docs.aws.amazon.com/aurora-dsql/latest/userguide/backup-aurora-dsql.html)
- Lambda: [pricing](https://aws.amazon.com/lambda/pricing/), [runtimes](https://docs.aws.amazon.com/lambda/latest/dg/lambda-runtimes.html), [Go packaging](https://docs.aws.amazon.com/lambda/latest/dg/golang-package.html), [quotas](https://docs.aws.amazon.com/lambda/latest/dg/gettingstarted-limits.html), [Function URLs](https://docs.aws.amazon.com/lambda/latest/dg/urls-configuration.html), [response streaming](https://docs.aws.amazon.com/lambda/latest/dg/configuration-response-streaming.html), [SnapStart](https://docs.aws.amazon.com/lambda/latest/dg/snapstart.html), [Managed Instances](https://docs.aws.amazon.com/lambda/latest/dg/lambda-managed-instances.html), [durable functions](https://docs.aws.amazon.com/lambda/latest/dg/durable-functions.html), [Lambda Web Adapter](https://github.com/awslabs/aws-lambda-web-adapter)
- API Gateway, DynamoDB, scheduling: [API Gateway pricing](https://aws.amazon.com/api-gateway/pricing/), [API Gateway quotas](https://docs.aws.amazon.com/apigateway/latest/developerguide/limits.html), [DynamoDB on-demand pricing](https://aws.amazon.com/dynamodb/pricing/on-demand/), [EventBridge pricing](https://aws.amazon.com/eventbridge/pricing/), [EventBridge Scheduler](https://docs.aws.amazon.com/scheduler/latest/UserGuide/what-is-scheduler.html), [SQS pricing](https://aws.amazon.com/sqs/pricing/), [River](https://riverqueue.com/docs)
- CloudFront: [pricing](https://aws.amazon.com/cloudfront/pricing/), [pay-as-you-go](https://aws.amazon.com/cloudfront/pricing/pay-as-you-go), [flat-rate plans](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/flat-rate-pricing-plan.html), [SaaS Manager announcement](https://aws.amazon.com/blogs/aws/reduce-your-operational-overhead-today-with-amazon-cloudfront-saas-manager), [managed certificates](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/managed-cloudfront-certificates.html), [quotas](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/cloudfront-limits.html), [invalidation by tags](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/invalidation-by-tags.html), [cache-tag announcement (2026-04)](https://aws.amazon.com/about-aws/whats-new/2026/04/cloudfront-invalidation-cache-tag/), [VPC origins](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/private-content-vpc-origins.html)
- S3, Route 53, ACM: [S3 pricing](https://aws.amazon.com/s3/pricing/), [Route 53 pricing](https://aws.amazon.com/route53/pricing/), [ACM pricing](https://aws.amazon.com/certificate-manager/pricing/)
- SES: [pricing](https://aws.amazon.com/ses/pricing/), [production access](https://docs.aws.amazon.com/ses/latest/dg/request-production-access.html), [quotas](https://docs.aws.amazon.com/ses/latest/dg/quotas.html), [event publishing](https://docs.aws.amazon.com/ses/latest/dg/monitor-using-event-publishing.html), [suppression list](https://docs.aws.amazon.com/ses/latest/dg/sending-email-suppression-list.html), [tenants](https://docs.aws.amazon.com/ses/latest/dg/tenants.html), [subscription management](https://docs.aws.amazon.com/ses/latest/dg/sending-email-subscription-management.html), [custom headers (2024-03)](https://aws.amazon.com/about-aws/whats-new/2024/03/amazon-ses-headers-sending-email), [header fields](https://docs.aws.amazon.com/ses/latest/dg/header-fields.html)
- Free tier: [free tier](https://aws.amazon.com/free/), [FAQ](https://aws.amazon.com/free/free-tier-faqs/), [credits announcement (2025-07)](https://aws.amazon.com/about-aws/whats-new/2025/07/aws-free-tier-credits-month-free-plan/)
- Third-party (used only where marked **[unverified]**): [Atlas issue on DSQL advisory locks](https://github.com/ariga/atlas/issues/3539), [DSQL pricing guide](https://awsfundamentals.com/blog/amazon-dsql-pricing-guide), [Aurora Serverless v2 summary](https://www.usage.ai/blogs/aws/rds/aurora-serverless-v2/), [Lambda arm64 pricing tiers](https://costgoat.com/pricing/aws-lambda)
