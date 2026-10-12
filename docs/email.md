# Email as a Separate Service

Email is evaluated here on its own, independent of where the app is hosted. Any host (a VPS, Cloudflare Workers, AWS Lambda, bunny.net) can call any of these providers over HTTPS or SMTP, so the hosting docs (`bunny.md`, `cloudflare.md`, `aws.md`) report cost **without** email, and this file supplies the email line.

**Researched:** 2026-09-29. 14 candidates, each researched from the vendor's official pricing and docs pages and then re-checked by a second agent whose job was to refute the first agent's numbers (the second agent corrected between 3 and 12 items per provider). A separate agent researched deliverability rules, and a critic pass looked for gaps. **Nothing was tested live**: no account was opened and no message was sent, so header pass-through, latency and inbox placement are still document-level claims. Prices are list prices in USD with no credits, and change often.

**Companion documents:** `prd.md` (§4 flows), `development-plan.md` (Phase 6), `workflows.md` (§10 unsubscribe), and the hosting docs. SMS is a separate service (`sms.md`).

---

## Summary

| Question | Answer |
|---|---|
| Cheapest clean option | **Amazon SES**: $0.10 per 1,000 with no monthly fee. $0.30 at 3k, $1 at 10k, $5 at 50k, $20 at 200k emails a month. Nothing else that passes every must-have comes close from 3k up. |
| Catches with SES | Manual sandbox exit (24-hour first response, AWS may decline without a reason), requests need SigV4 signing (no plain API key), bounce and complaint events arrive through SNS, no idempotency key, and enforcement is account-wide (see below). New accounts default to a $0.16 per 1,000 plan and must be switched to the $0.10 "a la carte" pricing. |
| Best fallback | **Resend**: GA, self-serve, no sandbox, plain bearer-token API. $20 flat up to 50k, then $125 at 200k. Its acceptable-use terms allow shutdown "without warning" above a 0.08% complaint or 4% bounce rate. |
| Free tiers | Nothing usable for real traffic. Every free tier is capped at 100 emails a day (Resend, Mailgun, Elastic, MailChannels, MailerSend, SendGrid trial) or is tiny (Postmark 100 a month, Scaleway 300 a month). A single 150-member announcement exceeds a 100-a-day cap. |
| Cloudflare Email Service | Still **beta**, transactional-only by its terms, unpublished daily quotas. $5 (Workers Paid) then $0.35 per 1,000. Not a clean candidate yet. |
| Self-hosting | Not viable for a new sender: outbound port 25 is blocked on the major hosts, and a new IP needs weeks of warm-up. |
| Biggest design lesson | Put login mail and group notifications on **separate sending subdomains and separate provider streams**. One organizer's complaint spike must not send magic links to spam. |

---

## What the workload needs

**Message streams** (from the PRD and development plan):

| Stream | Messages | Recipients | Needs |
|---|---|---|---|
| Auth (highest sensitivity) | Magic links for login, join-group, co-organizer invite; ownership-transfer notices | One person, who just asked for it | Seconds, not minutes (links expire in 15 minutes). No tracking. No unsubscribe header required (transactional). |
| Notifications | New meeting announced, reminders (24 hours ahead), changed or cancelled notices, organizer custom messages (3 per group per day) | Opted-in members and RSVPers, hundreds to a few thousand in a burst | One message per recipient (signed unsubscribe and cancel links). Our own `List-Unsubscribe` and `List-Unsubscribe-Post` headers. Group name as the sender's display name on one shared address. |
| Anonymous confirmations | RSVP confirmation, sent to an address a visitor typed into a form, unverified | One person | Abuse-resistant (anyone can trigger an email to any address). Cancel link. |

**Must-haves used to judge providers:** (1) HTTPS REST callable with plain `net/http`; (2) our own per-message `List-Unsubscribe` and one-click headers (no vendor-hosted unsubscribe page, for white-label); (3) per-message From display name on one shared domain; (4) link and open tracking can be turned off (rewritten links would leak the vendor's domain); (5) bounce and complaint feedback we can consume; (6) throughput for bursts of a few thousand messages in minutes with no multi-day approval or warm-up wall; (7) terms that allow opted-in group notifications and organizer-authored messages, not only "transactional" mail. **Deferred:** per-group sending domains for paid white-label groups (the PRD defers this; costs are recorded below).

**Volume model (assumption, to be refined in the cost model).** Per group per month, with 40 members and 1 meeting: about 36 announcements (90% opted in), 20 RSVP confirmations, 20 reminders (more if the group sends to all members), a few change notices, an organizer message or so, and about 8 magic links. That is roughly **125 emails per group per month**, plausibly 80 to 250. That gives about 12k a month at 100 groups, 125k at 1,000 groups and 1.25M at 10,000 groups. The PRD's planning figure of 50k a month therefore corresponds to about 400 groups, not the "roughly 100" that the research prompts assumed. The research priced 0, 3k, 10k, 50k and 200k a month; the model will extend those with the plan tables.

---

## Cost at each volume (verified list prices, no credits)

"Idle" means no account cost while no mail is sent, except where noted. "Cheapest plan that actually works" means it respects daily caps, required features and minimum fees (a free tier with a 100-a-day cap does not count as covering 3k with bursts).

| Provider | Tier | Idle | 3k | 10k | 50k | 200k | Main constraint |
|---|---|---|---|---|---|---|---|
| **Amazon SES** | Clean | $0 | **$0.30** | **$1** | **$5** | **$20** | Sandbox exit, SigV4, SNS plumbing |
| **Resend** | Clean | $0 | $20 | $20 | $20 | $125 | $20 minimum (free is 100 a day); strict complaint terms |
| **Mailgun** | Clean | $0 | $15 | $15 | $35 | $185 | No pay-as-you-go; plans end exactly at cap; one domain on Basic |
| **Twilio SendGrid** | Clean | $19.95 | $19.95 | $19.95 | $19.95 | $124.95 | No free tier (60-day trial); vetting; shared IPs slow for magic links |
| **SMTP2GO** | Clean | $0 | $15 | $15 | $30 | $160 | `List-Unsubscribe` undocumented; the static page shows $10 vs $15 (unresolved) |
| Cloudflare Email Service | Gated | $5 | $5 | $7.45 | $21.45 | $73.95 | Beta, transactional-only, unpublished quotas |
| Postmark | Gated | $0 | $15 | $15 | $55 | $224 | Announcements need its Broadcast stream and approval |
| MailerSend | Gated | $0 | $110 | $110 | $110 | $270 | Own unsubscribe headers only on the $110 Professional plan |
| Zoho ZeptoMail | Gated | $0 | $0.75 | $2.50 | $12.50 | $50 | Announcements disallowed; mandatory manual review |
| Scaleway TEM | Gated | $0 | $0.77 | $2.75 | $14.11 | $56.69 | Transactional-only; new-domain throttling; Paris region only |
| MailChannels | Gated | $0 | $10 | $10 | $15 | $60 | Terms ban white-label reselling; hard caps pause all sending |
| Brevo | Disqualified | $0 | $9 | $17 | $56 | $249 | Cannot set own unsubscribe headers or turn tracking off |
| Elastic Email | Disqualified | $0 | $19 | $19 | $19 | $79 | Forces its own unsubscribe link |
| Self-hosted MTA | Disqualified | $7.09 | $7.09 | $7.09 | $7.09 | $7.09 | Infrastructure floor only; port 25 blocked, warm-up wall |

Free tiers, for reference: SES none (the 3,000-a-month offer closed to new customers on 2026-07-21); Resend 3,000 a month capped at 100 a day; Mailgun 100 a day; SendGrid a 60-day trial at 100 a day; SMTP2GO 1,000 a month and 200 a day; Postmark 100 a month; Brevo 300 a day; MailerSend 500 a month and 100 a day; ZeptoMail 10,000 once, valid one month; Scaleway 300 a month; MailChannels 3,000 a month at 100 a day; Elastic 100 a day; Cloudflare none (requires Workers Paid).

**Illustrative extrapolation to the group counts** (linear from the plan tables; the model will redo this):

| Groups | Emails a month (at ~125 per group) | SES | Resend |
|---|---|---|---|
| 100 | ~12.5k | ~$1.25 | $20 |
| 1,000 | ~125k | ~$12.50 | ~$57.50 (Pro 100k $35 plus 25k at $0.90 per 1,000) |
| 10,000 | ~1.25M | ~$125 | ~$812 (Scale 1M $650 plus 250k at $0.65 per 1,000) |

---

## Provider notes

### Clean candidates

**Amazon SES.**
- **Price:** $0.10 per 1,000, no monthly fee. There is a worst-case data charge of about $0.77 at 200k, resting on a 32 KB message assumption and unverified billing wording. New accounts since 2026-07-21 default to the Essentials plan at $0.16 per 1,000, so switch to a la carte in the console or with `PutAccountPricingAttributes Plan=NONE`, once per region. Use a Paid account plan, not the Free plan, which closes the account at 6 months.
- **Access:** the sandbox allows 200 emails a day and 1 a second to verified recipients only. Production access is a manual review per region: choose Marketing or Transactional, give a website URL, acknowledge opt-in-only sending. First response is within 24 hours, and AWS can decline without detail. Verify the domain and set up bounce handling first, and ask for the quota you need in the same request. Post-sandbox quotas are unpublished ("varies by use case"); the commonly quoted 50,000 a day and 14 a second is third-party and unconfirmed.
- **Headers:** `SendEmail` v2 takes custom `Headers` (up to 15), and `List-Unsubscribe` and `List-Unsubscribe-Post` are allowed. **Do not use `ListManagementOptions` or the SES subscription page**: SES then overrides our headers and hosts its own page. `FromEmailAddress` takes `"Group Name <notifications@shared-domain>"` per request, and one verified domain covers all addresses on it. Non-ASCII names need RFC 2047 encoding.
- **Tracking:** off by default; keep open and click events unsubscribed.
- **Events:** configuration sets publish bounce, complaint, reject and delivery-delay events to SNS, EventBridge or Firehose, with message tags such as group id. From a non-AWS host that means an SNS HTTPS subscription with a confirmation handshake, or polling `ListSuppressedDestinations` with SigV4. Only hard bounces (and soft bounces once retries end) are emitted, and Gmail spam-button complaints are not reported to SES.
- **Auth:** requests need SigV4 signing with IAM keys. That is hand-rolled from Go, or SMTP with separate derived credentials, and SMTP header pass-through was not confirmed.
- **No idempotency key:** a retry after a timeout can send a duplicate magic link, so the outbox must dedupe.
- **Enforcement is account-wide:** a 5% hard-bounce rate triggers a review and 10% may pause **every** group; complaints trigger review at 0.1% and a pause at 0.5%. At low volume the rates swing fast, and anonymous RSVP addresses are the main bounce risk.
- **Domains and tenants:** no per-domain fee. Up to 10,000 identities per region. Optional SES tenants (per-group reputation isolation) cost $0.005 per tenant per month plus $0.005 per 1,000 emails on a la carte. This corrects `aws.md`, which said 1,000 tenants were included.
- **Other:** everything is regional (sandbox status, quotas, identities, suppression list), so pick one region. AWS may store and scan message content.

**Resend.**
- **Access:** self-serve and GA, with no sandbox and no production approval. Plain bearer-token REST, 10 requests a second per team.
- **Price:** the Free plan is 3,000 a month but capped at 100 a day (UTC). Pro is $20 for 50k (10 domains, 5 webhooks) and $35 for 100k, with $0.90 per 1,000 overage capped by default at 5x. Scale is $90 for 100k with 1,000 domains. At 200k the cheapest is Pro $35 plus overage, $125.
- **Fit:** it has no "transactional-only" rule, but the acceptable-use terms (2026-08-27) require recipients to have opted in, and set a complaint limit of 0.08% and a bounce limit of 4%, or the account "may be shut down without warning". Anonymous RSVPs to mistyped addresses are a gray area.
- **Domains:** Free 3, Pro 10, Scale 1,000, or +100 for $20 a month with no fixed ceiling.

**Mailgun.**
- **Price:** the pay-as-you-go Flex plan is deprecated. Basic $15 for 10k (1 sending domain, $1.80 per extra 1,000), Foundation $35 for 50k (1,000 domains) and $75 for 100k. Plans end exactly at the cap, and overage cannot be turned off.
- **Access:** new accounts need SMS verification to a real mobile number, and a card to add a custom domain.
- **Fit:** its policy is about explicit, provable consent (no transactional-only rule). Splitting login and notification mail across two domains needs Foundation ($35), not the $15 Basic.

**Twilio SendGrid.**
- **Price:** the free plan was retired in May 2025 (60-day trial only). Essentials $19.95 for 50k, $34.95 for 100k. Shared IPs only below the Pro plan ($89.95 for 100k).
- **Access:** upgrade needs domain authentication, card and 2FA, then a vetting step of up to 24 to 72 hours, and failed accounts are suspended.
- **Fit:** SendGrid itself says shared IPs are poorly suited to time-sensitive transactional mail. The 50k point lands exactly on the plan cap. A domain-per-user limit of 3,000 exists. `List-Unsubscribe-Post` signing was reported broken in 2019.

**SMTP2GO.**
- **Price:** Free is 1,000 a month and 200 a day. Starter 10K is $15 (the static page shows $10, unresolved), Starter 50K $30, Professional 100K $75. There is no pay-as-you-go, and overage is capped at 3x the plan.
- **Fit:** `List-Unsubscribe` and `List-Unsubscribe-Post` are not documented anywhere, though `custom_headers` accepts most headers. New paid accounts start with a 1,000-a-day limit until an automatic review passes, and sign-up rejects Gmail and other public-domain addresses.

### Gated: cheap or plausible, but blocked by policy or throughput

- **Cloudflare Email Service:** beta since 2026-04-16 and still beta. Requires Workers Paid and Cloudflare DNS. Terms limit it to transactional mail, quotas start "conservative" and are unpublished, events go only through Queues, and Cloudflare adds its own `Feedback-ID`, which may block per-group Gmail attribution. If REST sends count against the global 1,200-requests-per-5-minutes limit, a 3,000-message burst takes about 12 minutes. `List-Unsubscribe` is DKIM-signed (confirmed).
- **Postmark:** Basic $15 for 10k, $55 for 50k. Announcements belong on its Broadcast stream, and custom unsubscribe handling needs support approval. Accounts start in a manual review; sign-up needs a private-domain work email. The 200k figure ($224) depends on an unresolved overage rate ($205 to $224).
- **MailerSend:** cheap tiers reject custom headers (HTTP 422). Own `List-Unsubscribe` works only on Professional, from $110 a month, and event announcements count as marketing.
- **Zoho ZeptoMail (now "Zoho CPaaS"):** $2.50 per 10,000 emails, no minimum. The terms disallow "email announcements" as bulk, review is mandatory (2 to 6 business days), and the free 10,000 expires after one month.
- **Scaleway TEM:** about $0.28 per 1,000 after 300 free, Paris region only. Its policy allows only single-recipient transactional mail, new domains are throttled to roughly 100 a day for weeks, quotas need a Support request above a few thousand, and webhooks go through a Scaleway SNS topic.
- **MailChannels:** $10 for 10k, $15 for 50k, $60 for 200k, but the terms ban white-label commercial-email-service use with liquidated damages, and hitting the hard cap pauses all sending.

### Disqualified

- **Brevo:** cannot set custom `List-Unsubscribe` (its API only accepts non-standard headers) and tracking cannot be disabled. Event announcements count as marketing.
- **Elastic Email:** injects its own unsubscribe link into every message and it cannot be removed.
- **Self-hosted MTA (Postal, Stalwart, Postfix):** about $7.09 a month of VPS, but outbound port 25 is blocked on Hetzner (unblocking needs an account at least a month old with a paid invoice) and on AWS EC2, and no free VPS tier allows it, Hetzner's cheapest types were unavailable when checked, and a new IP needs 2 to 6 weeks of warm-up. Its cost excludes operations effort, and a burst on a cold IP defers or junks. It also fails must-have 6.

---

## Deliverability requirements

From official Google, Yahoo and Microsoft guidance and M3AAWG:

- **Gmail bulk-sender line:** about 5,000 messages a day to personal Gmail addresses from one primary domain, counting all subdomains, and the status is permanent once reached. At 50k a month with organizer bursts this can be crossed on a peak day, and at 1,000+ groups it will be. Build to bulk-sender rules from day one: SPF, DKIM and DMARC aligned (p=none is enough), one-click unsubscribe on notifications, unsubscribes honored within 2 days.
- **Spam rate:** stay under 0.1% in Gmail's Postmaster Tools and never reach 0.3%. At low volume one complaint is a large percentage.
- **Microsoft:** same 5,000-a-day trigger for Outlook.com and the same authentication requirements, in force since 2025-05-05. Non-compliant mail is junked first, then rejected.
- **New domains:** Google warns against bursts and sudden spikes without history. A 2,000-message burst from a young domain is the pattern to avoid: queue notification fan-out over 15 to 60 minutes and ramp up over weeks, and keep auth mail on its own unthrottled path.

**Recommended domain layout** (placeholder names):
- `auth.` subdomain: magic links, join, invites, ownership transfers only. Fixed sender name, its own DKIM, Return-Path and provider key. No tracking, no group-authored content.
- `mail.` subdomain: announcements, reminders, change notices, organizer messages **and anonymous RSVP confirmations** (anything whose content or trigger an organizer or a third party influences). Own DKIM and Return-Path.
- DMARC at the organizational domain: start `p=none` with reports, move to quarantine and then reject.
- Group name as the display name ("Hiking Club via Meetspace") on one fixed address, `Reply-To` to a monitored mailbox, our own per-recipient `List-Unsubscribe` and `List-Unsubscribe-Post` on all notifications (both must be inside the DKIM-signed header list).
- Do not over-segment at small volume; each stream needs steady traffic to earn reputation.

**Anonymous RSVP abuse.** The email-only RSVP form lets anyone trigger mail to any address. That means mail-bombing, spam-trap hits, bounces that count against the shared domain, and phishing content injected through the name and group fields in mail signed with our DKIM. Mitigations: rate limits per address and per IP, at most one confirmation per address per meeting, Turnstile, no free-text fields in mail to unverified addresses, MX and syntax checks (never SMTP probes), suppression of any address that hard-bounced, and identical responses whether or not an email is sent. Note that Turnstile's free plan allows 10 hostnames per widget and 20 widgets, so RSVP posts from many custom domains should go through one shared endpoint.

**Shared reputation.** Every group's complaints accrue to the same sending domain and IP pool, and complaint attribution per group requires our own tagging (Gmail `Feedback-ID`) and the provider's events. Auto-pause a group that crosses our own thresholds (for example above 0.1% complaints over 1,000 recipients, or above 2% hard bounces). Per-group sending domains pay off only for paid white-label groups or large steady senders, and their cost is small on SES (no per-domain fee) and Resend (+100 domains for $20 a month).

---

## Recommendation

1. **Primary: Amazon SES** on the a la carte plan in one region, sending the two streams from two subdomains with separate configuration sets, and our own headers. Cost is roughly $1.25, $12.50 and $125 a month at 100, 1,000 and 10,000 groups on the volume model above.
2. **Fallback: Resend** (or Mailgun or Postmark at low volume). It is clean on policy, needs no sandbox exit and is a plain bearer-token API. Keep the app's email sender behind one small interface so a swap is a config change.
3. **Consider a two-provider split.** The free tier of a clean second provider (Resend, Mailgun or Postmark, under 100 a day) can carry the low-volume auth stream. That isolates magic links from SES's account-wide suspension risk and provides failover, for $0 (or $20 if the daily cap is exceeded).
4. **Do not use** Cloudflare Email Service until it exits beta, quotas are published and the transactional-only terms are clarified. Skip the disqualified providers.

**Before committing, run these live tests** (the research could not):
- Send from SES and the fallback to a Gmail address and check `Authentication-Results` and that the DKIM `h=` list includes `List-Unsubscribe` and `List-Unsubscribe-Post`.
- Measure magic-link delivery latency.
- Submit the SES production-access request early, since the denial rate and time are only anecdotal.
- Confirm non-ASCII group names encode correctly.
- Confirm that a per-request configuration set works from a domain-inherited identity.

## Open questions

1. What is SES's real post-sandbox daily quota and burst rate for a new account, and how quickly is an increase granted?
2. Does SES DKIM-sign the custom `List-Unsubscribe` headers, and does SMTP header pass-through work if we avoid SigV4?
3. Which providers register or forward Yahoo's complaint feed and Microsoft's JMRP for our domain? Only SendGrid and Elastic say anything.
4. Does custom Return-Path or MAIL FROM work per provider (needed for SPF alignment)?
5. Resend overage rounding, SMTP2GO's $10 vs $15 Starter price, MailerSend's $110 tier, and Postmark's Basic overage rate need re-checking on live pages if any of them stays in contention.
6. Providers not checked: Azure Communication Services Email, Oracle Cloud Email Delivery, Mailtrap, Mailjet, and newer pay-as-you-go services (Emailit, AhaSend, MailPace). The first two are cheap per message and worth a quick look if SES is denied.
7. Per-group sending domains: extra-domain pricing is unknown for Mailgun, SendGrid, Brevo, Zoho and MailChannels.

## Corrections to the hosting docs

- `aws.md` said SES had no free tier beyond credits (correct), but also that tenants include 1,000 free. The price list charges $0.005 per tenant per month plus $0.005 per 1,000 emails, with 1,000 tenants included only on Enterprise. It also missed that new accounts default to the $0.16 Essentials plan, that SES has no idempotency key, and that the hosting cost tables should drop the email line.
- `cloudflare.md` correctly called Email Sending beta. Its comparison with Resend stands: at 50k a month it is $21.45 against $20.

---

## Sources

Official pages read by the research and verification agents are listed per provider in the appendix below.

### Official pages read, by provider

- **Resend** (confidence high): <https://resend.com/pricing>, <https://resend.com/pricing.md>, <https://resend.com/docs/knowledge-base/what-is-resend-pricing>, <https://resend.com/docs/api-reference/emails/send-email>, <https://resend.com/docs/api-reference/emails/send-batch-emails>, <https://resend.com/docs/api-reference/rate-limit>, <https://resend.com/docs/api-reference/errors>, <https://resend.com/docs/knowledge-base/account-quotas-and-limits>
- **Amazon SES** (confidence high): <https://aws.amazon.com/ses/pricing/>, <https://docs.aws.amazon.com/ses/latest/dg/pricing-plans.html>, <https://docs.aws.amazon.com/ses/latest/dg/request-production-access.html>, <https://docs.aws.amazon.com/ses/latest/dg/quotas.html>, <https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_SendEmail.html>, <https://docs.aws.amazon.com/ses/latest/dg/header-fields.html>, <https://docs.aws.amazon.com/ses/latest/dg/sending-email-suppression-list.html>, <https://docs.aws.amazon.com/ses/latest/dg/tenants.html>
- **Cloudflare Email Service (Email Sending)** (confidence medium): <https://developers.cloudflare.com/email-service/>, <https://developers.cloudflare.com/email-service/platform/pricing/>, <https://developers.cloudflare.com/email-service/platform/limits/>, <https://developers.cloudflare.com/email-service/api/send-emails/rest-api/>, <https://developers.cloudflare.com/email-service/api/send-emails/smtp/>, <https://developers.cloudflare.com/email-service/reference/headers/>, <https://developers.cloudflare.com/email-service/platform/event-subscriptions/>, <https://developers.cloudflare.com/email-service/concepts/suppressions/>
- **Postmark (ActiveCampaign)** (confidence high): <https://postmarkapp.com/pricing>, <https://postmarkapp.com/message-streams>, <https://postmarkapp.com/developer/api/email-api>, <https://postmarkapp.com/support/article/1084-how-does-the-account-approval-process-work>, <https://postmarkapp.com/terms-of-service>, <https://postmarkapp.com/support/article/1299-how-to-include-a-list-unsubscribe-header>, <https://postmarkapp.com/support/article/managing-your-own-unsubscribe-process>, <https://postmarkapp.com/developer/api/message-streams-api>
- **Mailgun** (confidence high): <https://www.mailgun.com/pricing/>, <https://documentation.mailgun.com/docs/mailgun/api-reference/send/mailgun/messages/post-v3--domain-name--messages>, <https://help.mailgun.com/hc/en-us/articles/203306610-Unsubscribe-Handling-Links>, <https://help.mailgun.com/hc/en-us/articles/6745531451547>, <https://help.mailgun.com/hc/en-us/articles/203068914-What-does-the-Free-plan-offer>, <https://help.mailgun.com/hc/en-us/articles/360048661093-Can-you-explain-pay-as-you-go-PAYG-billing>, <https://help.mailgun.com/hc/en-us/articles/202236504>, <https://help.mailgun.com/hc/en-us/articles/360012287493>
- **Twilio SendGrid** (confidence high): <https://sendgrid.com/pricing/ (301 chain ends at https://www.twilio.com/en-us/sendgrid, a product page, not the pricing page; https://sendgrid.com/en-us/free 301s to the pricing page)>, <https://www.twilio.com/en-us/products/email-api/pricing>, <https://www.twilio.com/en-us/changelog/sendgrid-free-plan>, <https://www.twilio.com/docs/sendgrid>, <https://support.sendgrid.com>, <https://www.twilio.com/en-us/legal/service-country-specific-terms/email>
- **Brevo (formerly Sendinblue)** (confidence medium): <https://www.brevo.com/pricing/>, <https://www.brevo.com/pricing/pay-as-you-go/>, <https://www.brevo.com/products/transactional-email/>, <https://www.brevo.com/legal/antispampolicy/>, <https://www.brevo.com/legal/termsofuse/>, <https://developers.brevo.com/reference/send-transac-email>, <https://developers.brevo.com/docs/api-limits>, <https://developers.brevo.com/docs/transactional-webhooks>
- **MailerSend** (confidence high): <https://www.mailersend.com/pricing>, <https://www.mailersend.com/help/plans-features-and-limits>, <https://www.mailersend.com/help/plan-add-ons>, <https://www.mailersend.com/help/custom-headers>, <https://www.mailersend.com/help/how-to-add-a-custom-unsubscribe-header>, <https://www.mailersend.com/help/domain-tracking-options>, <https://www.mailersend.com/help/suppression-list-managing>, <https://www.mailersend.com/help/sandbox-mode-and-the-professional-trial>
- **Zoho ZeptoMail (rebranded "Zoho CPaaS (formerly Zoho ZeptoMail)"; email channel only)** (confidence medium): <https://www.zoho.com/zeptomail/pricing.html (redirects to https://www.zoho.com/cpaas/pricing.html)>, <https://www.zoho.com/cpaas/transactional-email.html>, <https://www.zoho.com/cpaas/signup.html>, <https://www.zoho.com/cpaas/help/>, <https://www.zoho.com/cpaas/help/api/email-sending.html>, <https://www.zoho.com/cpaas/terms.html>, <https://www.zoho.com/cpaas/help/subscription.html>, <https://help.zoho.com/portal/en/kb/zoho-cpaas/faqs/getting-started>
- **Scaleway Transactional Email (TEM)** (confidence medium): <https://www.scaleway.com/en/pricing/managed-services/>, <https://www.scaleway.com/en/transactional-email-tem/>, <https://www.scaleway.com/en/docs/transactional-email/>, <https://www.scaleway.com/en/developers/api/transactional-email/>, <https://www-uploads.scaleway.com/Scaleway_TEM_Politique_Antispam_en_17f76aa5ec.pdf>, <https://tem.s3.fr-par.scw.cloud/antispam_policy.pdf>
- **Elastic Email** (confidence high): <https://elasticemail.com/email-api-pricing>, <https://elasticemail.com/email-api>, <https://elasticemail.com/developers/api-documentation/rest-api>, <https://api.elasticemail.com/public/v4/swagger>, <https://api.elasticemail.com/public/help>, <https://help.elasticemail.com/en/articles/3331811-elastic-email-pricing-plans>, <https://help.elasticemail.com/en/articles/2446055-what-are-sending-limits-for-new-accounts>, <https://help.elasticemail.com/en/articles/4808492-unsubscribe-settings>
- **MailChannels (Email API)** (confidence medium): <https://www.mailchannels.com/pricing/>, <https://www.mailchannels.com/email-api/>, <https://docs.mailchannels.com/email-api/billing/pricing>, <https://docs.mailchannels.com/email-api/overview>, <https://docs.mailchannels.com/email-api/domain-lockdown>, <https://docs.mailchannels.com/email-api/custom-headers>, <https://docs.mailchannels.com/email-api/unsubscribe>, <https://docs.mailchannels.com/email-api/custom-tracking>
- **SMTP2GO** (confidence medium): <https://www.smtp2go.com/pricing/>, <https://www.smtp2go.com/terms/>, <https://developers.smtp2go.com/>, <https://developers.smtp2go.com/docs/rate-limiting>, <https://developers.smtp2go.com/docs/getting-started>, <https://developers.smtp2go.com/reference/send-standard-email>, <https://developers.smtp2go.com/reference/send-email-batch>, <https://developers.smtp2go.com/docs/webhooks-overview>
- **Self-hosted MTA (Postal, Stalwart, or Postfix on a VPS)** (confidence medium): <https://docs.postalserver.io/>, <https://github.com/postalserver/postal>, <https://stalw.art/docs/>, <https://github.com/stalwartlabs/stalwart>, <https://www.postfix.org/>, <https://docs.hetzner.com/cloud/servers/faq/>, <https://docs.hetzner.com/general/infrastructure-and-availability/price-adjustment/>, <https://www.hetzner.com/cloud/cost-optimized/>

### Deliverability and architecture

- <https://support.google.com/a/answer/81126>
- <https://support.google.com/mail/answer/81126>
- <https://support.google.com/a/answer/14229414>
- <https://support.google.com/mail/answer/14668346>
- <https://support.google.com/mail/answer/6254652>
- <https://support.google.com/a/answer/175365>
- <https://senders.yahooinc.com/best-practices/>
- <https://senders.yahooinc.com/faqs/>
- <https://support.microsoft.com/en-us/outlook/fix-ndr-error-550-5-7-515-in-outlook-com>
- <https://substrate.office.com/ip-domain-management-snds/Postmaster/Policies>
- <https://techcommunity.microsoft.com/blog/microsoftdefenderforoffice365blog/strengthening-email-ecosystem-outlook%E2%80%99s-new-requirements-for-high%E2%80%90volume-senders/4399730 (body not fetchable; search summary only)>
- <https://learn.microsoft.com/en-us/defender-office-365/safe-links-about>
- <https://dmarcian.com/microsoft-enforces-spf-dkim-dmarc/ (third party)>
- <https://www.rfc-editor.org/rfc/rfc8058.html>
