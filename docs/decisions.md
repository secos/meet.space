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
