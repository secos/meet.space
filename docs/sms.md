# SMS as a Separate Service

SMS is evaluated here on its own, like email (`email.md`): any host can call any of these providers over HTTPS, so hosting costs exclude it. Unlike email, SMS is not part of the PRD today. The PRD is email-only ("no passwords, minimal interaction"), so this file answers whether SMS is worth adding, what it would cost, and what it would force us to build.

**Researched:** 2026-09-29. 12 providers, each researched from the vendor's official pricing and docs pages and then re-checked by a second agent trying to refute the numbers (8 to 16 corrections per provider). Separate agents covered US and international compliance rules and fraud, and web push as a free baseline, and a critic pass looked for gaps. **Nothing was tested live** (no account opened, no message sent). Several vendor pages were unreachable to the fetch tools (some returned HTTP 403), so a number of carrier fees come from search excerpts, and those are marked. Prices are list prices in USD with no taxes or credits, and change often. Carrier fees rose on 2026-10-01 (Verizon $0.0045 to $0.0050), two days after the research date, and most records don't include it.

**Companion documents:** `email.md` (the primary channel), `prd.md`, `workflows.md`.

---

## Summary

| Question | Answer |
|---|---|
| What does SMS cost per message? | About **$0.0078 to $0.0125 all-in in the US** (base price plus carrier pass-through fees). Email on SES is about $0.0001, so SMS is roughly **80 to 125 times** more. Outside the US it is far higher: about $0.03 to $0.11 per message in the UK, Germany, Australia, India and Brazil. |
| What does it cost at our volumes? | About **$11 to $28 at 1,000 messages a month, $43 to $91 at 5,000, and $160 to $330 at 20,000**, for the viable providers. The cheapest are Bird and Telnyx (about $0.0078 per message), then AWS, Sinch, Twilio (about $0.0125). |
| Is there a fixed cost? | Yes, and it matters more than the per-message price at our early volumes. Once registered, **about $1 to $4 a month** accrues with zero messages (number rental plus, for 10DLC, a campaign fee). Launch needs about **$25 to $40 in cash**, a legal entity with an EIN (or equivalent), a live public website with a privacy policy and an opt-in page, and **1 to 3 weeks** of carrier approval. |
| Cheapest compliant sender type | **Toll-free**: no brand or campaign fees, free verification, $1 to $2 a month for the number, 3 to 5 business days review. 10DLC "Low Volume Mixed" is cheaper per message on some providers above about 1,000 messages a month. |
| Best free alternative | **Web push** through the PWA: $0 at every volume, no registration, no carrier fees. It can replace SMS for reminders and organizer messages for members who enable it. It cannot replace SMS or email for login, and it has no replies. |
| Should SMS be in v1? | **No.** Keep email for login and web push for free reminders. Add SMS later only as an opt-in reminders add-on for members who have neither push nor reliable email, and never as a public "text me a login code" form (see fraud below). |
| If we do add it | Twilio (highest per-message price, best tooling, fastest approval) or Telnyx or Bird (about 40% cheaper per message, slower or less certain approval). Send from toll-free, and start only once the feature is actually built. |

---

## What SMS would be used for

None of these is committed. Each is priced separately because they have very different risk.

| Use | Description | Comment |
|---|---|---|
| 1. Login or join codes by SMS | An optional alternative to the email magic link. Needs a phone number as an identity. | The highest-risk use: a public form that sends an SMS is the target of **SMS pumping fraud**. Not recommended. |
| 2. Meeting reminders and change notices | To members and RSVPers who explicitly opted in to SMS. | The natural fit. Free alternative: web push. |
| 3. Organizer custom messages | Up to 3 per group per day, to opted-in members. | Legally the trickiest: who is the "sender" (see consent). |
| 4. Two-way replies | Reply YES or NO to RSVP. STOP and HELP are required by carriers. | Adds inbound message fees and an inbound webhook. |

---

## Cost at each volume

Assumptions (from the research): US recipients only, one GSM-7 segment per message (a realistic reminder with a group name, title, time, link and "Reply STOP" is often 2 segments, which would double per-message cost), 1% of volume as inbound STOP replies (2% for Bandwidth; a 10% reply rate for two-way RSVP would add about $20 a month at 20k on Twilio), the cheapest US sender type that the provider would actually approve, and one-time fees amortized over 12 months. Tier reflects must-have fitness and eligibility, not price.

| Provider | Tier | Idle | 1k | 5k | 20k | Sender and notes |
|---|---|---|---|---|---|---|
| **Twilio** | Viable | $2.15 | $14.73 | $65.03 | $253.67 | Toll-free. About $0.0125 per message. Free verification. Best tooling. |
| **Telnyx** | Viable, slow onboarding | $1.18 | $10.65 | $44.01 | $171.60 | Toll-free at 1k, 10DLC Low Volume Mixed at 5k, Mixed at 20k. Account tier review unpublished. |
| **Bird** (MessageBird) | Viable, low confidence | $4.13 | $11.90 | $42.98 | $159.53 | 10DLC Low Volume. Inbound price and toll-free fee unpublished. |
| **AWS End User Messaging** | Viable | $2.00 | $14.14 | $62.72 | $244.89 | Toll-free. SigV4 signing, SNS for webhooks, mandatory sandbox, up to 15 business days. |
| **Sinch** | Viable | $2.17 | $14.27 | $62.68 | $244.23 | Toll-free. Carrier fees come from pages the fetch tool couldn't read. |
| **Infobip** | Viable | $3.13 | $15.64 | $65.71 | $253.46 | 10DLC. Number rental unpublished and excluded (about $1 more). |
| **Vonage** | Viable, expensive | $4.21 | $20.39 | $85.12 | $327.84 | EUR-first pricing. US base price unconfirmed ($0.0119 used; a $0.0081 quote would put it level with Twilio). |
| Bandwidth | Sales-gated | $0.35 | $11.86 | $47.22 | $189.82 | Needs a sales-enabled messaging account, 12-month contract, no fraud protection or STOP support on long codes. Number price is from 2021. |
| ClickSend | Poor fit | $3.53 | $28.13 | $90.53 | $275.53 | Rate follows the size of your last top-up ($100 to $2,000). Pauses SMS with URLs for new customers. |
| Brevo SMS | Poor fit | $0 | $10.90 | $54.50 | $218 | Toll-free only, **4 to 6 week** approval, no fraud protection, credits can change ratio "with retroactive effect". |
| Plivo | Disqualified | $1,000 | $1,004 | $1,021 | $1,084 | SMS is sales-only, with a **$1,000 a month minimum** and no multi-tenant traffic. |
| Firebase phone auth | Codes only | $0 | $7 | $47 | $197 | Verification codes only. No reminders, no replies, no server-side send or webhooks. |

The ranking is not a recommendation. At 1,000 messages a month the difference between the top eight rows is about $4, so **eligibility, approval speed and fraud controls should decide, not price**. Bird's lead at 5k and 20k over Telnyx is thin and low confidence.

**What is left out of every row:** the cost of building it (consent records, STOP and HELP handling, natural-language opt-outs, quiet hours, a GSM-7 guard, a spend circuit breaker: several dev-days, unestimated), taxes and VAT (Vonage, Infobip and Brevo exclude them), and the Oct 2026 carrier fee rise.

**Idle and launch cost.** A provisioned sender costs about $1.10 to $4.30 a month with no traffic (number rental, plus $1.50 to $2 a month for a 10DLC campaign billed for at least 3 months at some providers). Cash to launch is about $21 to $40 (Twilio $21 to $36; Telnyx $25 to $40; Vonage about $25). Dropping the number and campaign takes you back to $0 but costs $15 to re-vet and restarts the wait. **Do not register until the feature is built**, because approval needs a live site and an opt-in page.

**Non-US per-message base prices** (list price for one outbound message, before any carrier surcharges the pages don't show):

| Country | Twilio | Telnyx | AWS | Sinch | Infobip | Bird |
|---|---|---|---|---|---|---|
| Canada | $0.0083 (about $0.015–0.017 all-in) | $0.0105 | $0.0129 | $0.0078 | $0.0093 | $0.0134 |
| United Kingdom | $0.056 | $0.050 | $0.050 | $0.044 | $0.044 | $0.050 |
| Germany | $0.112 | $0.106 | $0.107 | $0.088 | $0.088 | $0.080 |
| Australia | $0.0515 | $0.046 | $0.040 | $0.033 | $0.027 | $0.033 |
| India | $0.083 | $0.068 | $0.071 | $0.066 | $0.065 | $0.065 |
| Brazil | $0.060 | $0.054 | $0.030 | $0.033 | $0.029 | $0.033 |

For an audience outside the US and Canada, SMS is not a "small opted-in subset" cost. At Twilio's rates 5,000 messages a month is about $280 in the UK and $560 in Germany.

---

## Compliance: the fixed cost of even the cheapest sender

**US A2P registration.** All business SMS to US numbers must be tied to a registered brand and campaign (10DLC) or a verified toll-free number. Unregistered traffic is blocked, and still billed.
- **Toll-free (cheapest, recommended):** no brand or campaign fees, verification free on Twilio, Plivo, Sinch and Bandwidth (not confirmed on Telnyx). Twilio reviews within about 3 to 5 business days, Telnyx 5 business days but often 1 to 2 weeks, AWS up to 15. Unverified numbers cannot send to the US or Canada. Default throughput is 3 messages a second.
- **10DLC:** brand $4.50 one-time (a Standard brand with third-party vetting is about $46), campaign vetting $15 per submission (re-charged on resubmission), then a monthly campaign fee: $1.50 for "Low Volume Mixed", $10 for most other types (billed at least 3 months up front at some providers). Low Volume Mixed is capped at about 2,000 segments a day on T-Mobile and 75 messages a minute on AT&T. Approval is 1 to 3 weeks, plus 1 to 2 weeks for each rejection. Registration happens per provider, and moving later costs $0.50 per campaign and some restarts.
- **Short code:** $500 to $1,500 a month, 6 to 16 weeks. Not viable.
- **Identity:** a legal business identity (EIN) is required for every toll-free and standard 10DLC route since 2026-02-17, unless registering as a sole proprietor. Sole-proprietor routes have hard caps, exclude login codes at Twilio, and Telnyx rejects "group", "club" and ".org" names. A new EIN can take 30 to 90 days to propagate into verification databases. The EIN application online needs a US-based principal place of business and a responsible party with an SSN or ITIN; non-US applicants must apply by phone, fax or mail. **Open question:** whether meetspace's legal entity has a US EIN or can use a foreign business number.
- **A live public website** with the business name, address and contact, a privacy policy stating that mobile numbers are not shared or sold, terms, and a publicly viewable opt-in page (or screenshot). Toll-free verification has required separate Privacy Policy and Terms URLs since 2026-09-15.

**Consent.**
- A member opting in through the authenticated profile or RSVP screen is the compliant model, with an **unchecked, optional, SMS-specific checkbox** separate from "I agree to the terms", naming the brand and message type and including "Message and data rates may apply", frequency and "Reply STOP to opt out". A confirmation text must follow before any other message.
- **Keep records:** timestamp, the exact page version, IP and number for every consent.
- A **public "enter your phone number" RSVP form** is an accepted opt-in mechanism only with those disclosures, and the core risk is **consent from the wrong person** (a typo, a prank or a reassigned number).
- **Organizers must not type members' numbers.** For organizer custom messages the platform remains liable under the FCC's 2014 GroupMe ruling even if the organizer conveyed consent. Carriers also object to messages sent "on behalf of a third party": Plivo won't onboard multi-tenant traffic, Telnyx expects a brand per end-user, and AWS toll-free rejects it. If per-organizer brands were required, each would cost at least $4.50 plus $15 plus $1.50 a month and use 3 becomes uneconomic. Treat use 3 as conditional on legal review.
- **TCPA:** $500 per violating message, up to $1,500 if willful. Revocation must be honored by any reasonable means within 10 business days (effective 2025-04-11). A newer FCC draft is on the 2026-09-30 agenda and may tighten it. Several states (Florida, Oklahoma, Maryland, Oregon, Texas) add their own rules.
- **STOP handling:** carriers require STOP and HELP. Twilio and Telnyx only recognize STOP as the whole message ("STOP PLEASE" is not blocked), while CTIA expects natural-language opt-outs to be honored, so that part is on our side. Bandwidth offers no opt-out support on long codes. HELP is not forwarded to your webhook on Twilio unless Advanced Opt-Out is on.
- **Content:** no shorteners (public ones get blocked), no cannabis, gambling, SHAFT or crypto content. Emoji or curly quotes force UCS-2 (70 characters a segment) and can double or triple cost, so strip them from group names. Some providers, and ClickSend in particular, pause messages with URLs for new customers.

**Outside the US.** Canada's CASL needs consent and a working unsubscribe (fines up to CA$10M). The UK has no sender-ID registry (Ofcom decided against one in July 2026). Germany and France need prior consent for marketing texts. Australia requires alphanumeric sender IDs to be registered from 2026-07-01. India needs DLT registration of entity, sender and templates. Details came from law-firm summaries, not primary sources, so consult counsel before a non-US launch.

**SMS pumping fraud.**
- **How it works:** attackers script a public form that sends an SMS, using numbers they control or that sit on colluding operators, and you pay for every message. Vendor figures cite millions in blocked fraud, but there is no audited number for small senders and reports of five-figure overnight bills are anecdotal.
- **No provider offers a hard SMS spend cap.** Twilio's Usage Triggers only alert (though a "circuit breaker" callback can suspend a subaccount, and a prepaid balance reaching $0 suspends the project). Telnyx requires a destination allow-list and a daily spend limit. AWS has an enforced monthly limit. Plivo has a $1,000 default cap. Bandwidth's terms make you liable with no product.
- **Standard mitigations (mostly free):** a country allow-list (default to the sender's home country), rate limits per number, IP and account, Turnstile before any send, verify-then-send only to numbers already confirmed, and never to send SMS from a public unauthenticated form. Paid protection costs extra (AWS Protect about $0.01 a message, nearly doubling cost; Vonage Fraud Defender Advanced $47 a month; Twilio Verify's Fraud Guard is bundled into its $0.05 per success price).
- **Design consequence:** login by SMS is the pumping target, so keep it optional and only for accounts whose email is already verified, or skip it entirely. Reminders and notices go only to numbers already confirmed by a code, which means no public trigger exists.

---

## Provider notes

**Twilio** (highest cost, best tooling). Base $0.0083 plus carrier fees $0.0035 to $0.005, about $0.0125 all-in. Toll-free number $2.15 a month, verification free (needs an EIN unless a sole proprietor, a live website, separate Privacy and Terms URLs; 3 to 5 business days). 10DLC Low Volume Standard brand $4.50, campaign $15 plus $1.50 a month. Self-serve, but the upgrade asks for identity details and sometimes a government ID, and the trial (100 messages to verified numbers) is nearly useless for testing. Verify (OTP-only, no registration) costs $0.05 per success plus SMS, about $0.0625, so it only beats your own registered number below about 53 OTPs a month. The `MaxPrice` parameter is obsolete, and there is no native hard spend cap. Inbound STOP messages are billed like any inbound message. All must-haves confirmed.

**Telnyx** (cheapest headline). Base $0.004 (toll-free $0.0055) plus carrier fees at a published blended $0.0038, about $0.0078 on 10DLC or $0.0093 toll-free. Number $1.10 a month. Fully pay-as-you-go with no minimum. The catch is **account gating**: a Paid-tier account cannot use 10DLC or toll-free verification, you need the Verified tier (KYC plus an AI-agent evaluation) and the review time is unpublished. New accounts start in a "Pretrial" tier capped at 10 messages a day. Verification fee not published (assume $0, unconfirmed). Fraud controls are good: a required destination allow-list and a daily spend cap.

**Bird (MessageBird).** Headline $0.0035 plus carrier fees, about $0.0077. Number $1 a month, 10DLC brand $4.50, campaign $15 plus $1.50 a month. Default-deny destination allow-list. Inbound price and toll-free fee are unpublished, US STOP automation is unconfirmed, and timelines are unpublished, so treat its cost lead as "tied with Telnyx" at medium confidence.

**AWS End User Messaging SMS.** $0.00774 plus $0.00421 carrier fees. Toll-free number $2 a month. Every request needs SigV4 signing (no API key), delivery receipts arrive through SNS or Firehose (no webhooks), a mandatory sandbox caps new accounts at $1 a month until an AWS Support case is approved, and toll-free registration takes up to 15 business days. The AWS records also conflict between sources on the US price ($0.0119 vs $0.0129).

**Sinch, Infobip, Vonage.** Viable and self-serve but no cheaper than Twilio. Sinch's carrier fees and Infobip's number prices are not on their public pages. Vonage lists prices in EUR and its US base price is unresolved.

**Excluded or poor fits.** Plivo ($1,000 a month minimum, sales-only, no multi-tenant), Bandwidth (contract-gated, no fraud product, suspect fees), ClickSend (top-up-tiered rates, URL pausing, no OTP product), Brevo (toll-free only, 4 to 6 weeks approval, no fraud protection), Firebase (codes only). Email-to-SMS carrier gateways and self-hosted Android SIM gateways are non-compliant for A2P traffic and were rejected.

---

## Web push: the free baseline

Web Push (W3C Push API, VAPID) costs **$0 at any volume**: no push-service account, no per-message fee, no registration and no carrier fees. The abuse surface is limited to spamming our own opted-in users.

- **Reach:** works in a normal tab on Android Chrome, Firefox and Samsung Internet, and on desktop Chrome, Edge, Firefox and Safari 16+. **iOS and iPadOS 16.4+ only deliver push to a PWA installed to the Home Screen**, opened from the icon, after a tap on an "Enable" button. There is no programmatic install prompt on iOS. Since iOS 17.2 the session cookie carries over to the installed app.
- **Fit:** good for reminders and change notices (uses 2 and 3): schedule at T-24h, use a TTL until the meeting starts, and a `Topic` to replace superseded notices. **It cannot replace SMS or email for login (use 1)**, because a push subscription only exists after someone has already opened the site, granted permission and registered a service worker. It has no replies or STOP (use 4), though a notification click can open the RSVP page, and Chrome, Edge and Firefox support "Going" and "Can't make it" action buttons (not Safari or iOS).
- **Custom domains:** subscriptions are per origin, so a member subscribed on the platform domain gets nothing from a group's custom domain and vice versa. Each white-label domain needs its own service worker, manifest and permission prompt (one VAPID key pair can be shared).
- **Delivery caveats:** no delivery receipts (a 201 only means the push service accepted it); 4 KB payload limit; Chrome auto-revokes permission for low-engagement sites (installed web apps are exempt) and applies rate limits since January 2026; Safari can delete a tab-based subscription for infrequently visited sites; every push must show a notification.
- **Effort:** the research estimates 2 to 4 dev-days plus 1 to 2 days of device testing (its own estimate, unsourced). Go library `github.com/SherClockHolmes/webpush-go` (v1.4.0, MIT).
- **Unverified:** whether iOS delivers Home Screen web push in the EU today (Apple's statement doesn't mention push), real opt-in rates, and what share of members will complete the iOS install.

**Positioning:** web push is the free first channel for reminders, email is the guaranteed fallback and the only login mechanism, and paid SMS is for the residual audience with neither. The break-even (how many SMS-reachable members justify about $2 to $4 a month plus $25 to $40 to launch plus the dev days) has no estimate yet.

---

## Recommendation

1. **Do not add SMS to v1.** Email carries login and notifications, and the PWA carries web push for members who enable it. Both cost almost nothing per message.
2. **If SMS is added later, scope it to reminders only** (use 2), for members who opt in from their authenticated profile, never a public login form, with organizer messages (use 3) only after legal review.
3. **Choose the provider on eligibility and approval risk, not on price.** Twilio if tooling and a 3 to 5 day toll-free verification are worth about 60% more per message; Telnyx or Bird otherwise, accepting slower or less certain onboarding. Send from a toll-free number.
4. **Do not register until the feature exists**, since idle registration costs $1 to $4 a month and approval needs a live site and opt-in page.
5. **For the cost model:** use $0.0078 to $0.0125 per US message all-in, $1 to $4 a month fixed, and $25 to $40 to launch. The volume is a small opted-in share of members, so the fixed fees dominate until a few thousand messages a month. Outside the US the per-message cost is 4 to 9 times higher.

## Open questions

1. Does meetspace's legal entity have a US EIN or a business number accepted by TCR and toll-free verification? This gates which providers and sender types are available at all.
2. Who is the legal sender of record for organizer messages, and is a per-organizer brand required?
3. Which audience countries matter? WhatsApp Business messaging is likely much cheaper for India, Brazil, Germany, the UK and Australia (recollection, unverified), and RCS is another route. Neither was researched.
4. Non-SMS login alternatives (passkeys, TOTP, Sign in with Apple or Google, Turnstile plus email) remove the pumping vector and the phone-number requirement at $0 and deserve a comparison before any SMS login is considered.
5. Number normalization: the same carrier mix, the 2026-10-01 Verizon and 2026-11-01 ClearSky fee rises, a single Low Volume Mixed eligibility rule at 20k, and 1.5 to 2 segments per message would all change the rankings and haven't been applied.
6. Unresolved data conflicts: Bandwidth's 10DLC fees are almost exactly double the TCR schedule, Vonage's US price ($0.0119 vs $0.0081), AWS's US price, Telnyx's toll-free verification fee, and Twilio's toll-free "pending" sending caps.
7. Providers not researched: SignalWire, Azure Communication Services SMS, Telegram Gateway, WhatsApp Business, RCS.

---

## Sources

Official pages read by the research and verification agents are listed per provider in the appendix below.

### Official pages read, by provider

- **Twilio (Programmable Messaging + Verify)** (confidence high): <https://www.twilio.com/en-us/sms/pricing/us>, <https://www.twilio.com/en-us/verify/pricing>, <https://www.twilio.com/en-us/phone-numbers/a2p-10dlc>, <https://help.twilio.com/articles/1260803965530>, <https://www.twilio.com/docs/trust-hub/registrations/a2p-10dlc-brand>, <https://www.twilio.com/docs/messaging/compliance/toll-free/api-onboarding>, <https://help.twilio.com/articles/13264118705051>
- **Telnyx** (confidence medium): <https://telnyx.com/pricing/messaging>, <https://telnyx.com/pricing/numbers>, <https://telnyx.com/pricing/verify-api>, <https://telnyx.com/pricing>, <https://telnyx.com/pricing.md>, <https://api.telnyx.com/v2/pricing/products/sms-api>, <https://api.telnyx.com/v2/pricing/products/messaging-outbound>
- **Plivo** (confidence high): <https://www.plivo.com/sms/pricing/us/>, <https://www.plivo.com/pricing/>, <https://www.plivo.com/docs/messaging/sms-availability>, <https://www.plivo.com/docs/faq/account/plans>, <https://www.plivo.com/docs/messaging/api/10dlc/overview>, <https://www.plivo.com/docs/messaging/api/10dlc/brand>, <https://www.plivo.com/docs/messaging/api/toll-free-verification>
- **Vonage (Ericsson) SMS API and Verify** (confidence medium): <https://www.vonage.co.uk/communications-apis/pricing/>, <https://www.vonage.co.uk/communications-apis/sms/pricing/>, <https://dashboard.nexmo.com/download_pricing>, <https://dashboard.vonage.com/download_pricing/sms>, <https://www.vonage.co.uk/communications-apis/verify/pricing/>, <https://www.vonage.co.uk/communications-apis/fraud-defender/>, <https://www.vonage.co.uk/communications-apis/sms/features/opt-out-assist/>
- **Sinch** (confidence medium): <https://sinch.com/pricing/sms/>, <https://sinch.com/phone-numbers/toll-free/text-messaging/>, <https://sinch.com/products/10dlc/>, <https://sinch.com/blog/us-short-codes-vs-long-codes-vs-toll-free-numbers-whats-the-difference/>, <https://sinch.com/apis/messaging/sms/>, <https://sinch.com/verification-api/>, <https://developers.sinch.com/docs/sms/getting-started>
- **AWS End User Messaging SMS (formerly Pinpoint SMS) and Amazon SNS SMS** (confidence high): <https://aws.amazon.com/end-user-messaging/pricing/>, <https://aws.amazon.com/end-user-messaging/faqs/>, <https://aws.amazon.com/sns/sms-pricing/>, <https://aws.amazon.com/sns/pricing/>, <https://d1.awsstatic.com/onedam/marketing-channels/website/aws/en_US/business-applications/approved/documents/End-User-Messaging-SMS-Prices.ebc340b4d416d90832dd59629c4792b0deb6f8bc.csv>, <https://s3.amazonaws.com/aws-messaging-pricing-information/TextMessageOutbound/prices.json>, <https://s3.amazonaws.com/aws-messaging-pricing-information/TextMessageInbound/prices.json>
- **Bandwidth** (confidence medium): <https://www.bandwidth.com/pricing/>, <https://www.bandwidth.com/products/sms-messaging/>, <https://www.bandwidth.com/products/messaging-apis/>, <https://www.bandwidth.com/products/a2p-10dlc/>, <https://www.bandwidth.com/products/toll-free-sms/>, <https://www.bandwidth.com/support/en/articles/12823178-carrier-surcharges>, <https://www.bandwidth.com/support/en/articles/12823086-10dlc-fees>
- **Bird (formerly MessageBird)** (confidence medium): <https://bird.com/en-us/pricing/sms>, <https://bird.com/en-us/pricing/sms/us>, <https://bird.com/en-us/pricing/sms/fees.md>, <https://bird.com/en-us/pricing/numbers>, <https://bird.com/en-us/pricing/verify>, <https://bird.com/en-us/pricing>, <https://bird.com/docs/guides/sms/sending-sms>
- **Infobip** (confidence medium): <https://www.infobip.com/sms/pricing>, <https://www.infobip.com/pricing>, <https://www.infobip.com/policies/north-america-pass-through-fees>, <https://cdn-web.infobip.com/uploads/2025/08/infobip-usa-ca-operator-fees.xlsx>, <https://www.infobip.com/docs/essentials/getting-started/free-trial>, <https://www.infobip.com/docs/essentials/getting-started/paying-account>, <https://www.infobip.com/docs/usa-sender-registration>
- **ClickSend (Sinch ClickSend)** (confidence medium): <https://www.clicksend.com/us/pricing/>, <https://rest.clicksend.com/v3/pricing/US>, <https://www.clicksend.com/us/ajax/pricing/?selected-country=US&currency=USD>, <https://www.clicksend.com/js/pricing-v2.js>, <https://dashboard.clicksend.com/signup>, <https://help.clicksend.com/en/articles/44202-sms-mms-carrier-fees>, <https://help.clicksend.com/en/articles/67745-understanding-us-sms-mms-pricing>
- **Brevo SMS** (confidence medium): <https://www.brevo.com/pricing/pay-as-you-go/?selected=sms>, <https://billing-v2.brevo.com/billing/api/v2/addons/sms/currency/USD>, <https://www.brevo.com/features/sms-marketing/>, <https://help.brevo.com/hc/en-us/articles/208717449-Supported-countries-and-pricing-for-SMS-messages>, <https://help.brevo.com/hc/en-us/articles/28255350696466-Register-a-Sender-ID-to-send-SMS-messages>, <https://help.brevo.com/hc/en-us/articles/11884482521234-Country-specific-guidelines-and-restrictions-for-SMS-messages>, <https://help.brevo.com/hc/en-us/articles/8840663406738-Comply-with-the-United-States-SMS-marketing-regulations-with-our-compliance-feature>
- **Firebase Authentication phone sign-in / Google Cloud Identity Platform** (confidence medium): <https://firebase.google.com/pricing>, <https://cloud.google.com/identity-platform/pricing>, <https://firebase.google.com/docs/auth/limits>, <https://firebase.google.com/docs/auth/web/phone-auth>, <https://firebase.google.com/docs/auth/android/phone-auth>, <https://firebase.google.com/docs/auth/faq-and-troubleshooting>, <https://firebase.google.com/support/faq>

### Compliance and fraud

- <S1 TCR Fees and Pricing General Guide, last updated 2026-08-13 (primary): https://www.campaignregistry.com/Assets/TCR%20Fees%20and%20Pricing.pdf>
- <S2 Twilio US SMS pricing (base, carrier fees, numbers, short codes): https://www.twilio.com/en-us/sms/pricing/us>
- <S3 Twilio 10DLC product page (fees, timeline, sole-prop): https://www.twilio.com/en-us/phone-numbers/a2p-10dlc>
- <S4 Twilio Programmable Messaging and A2P 10DLC docs (brand types, Verify exemption): https://www.twilio.com/docs/messaging/compliance/a2p-10dlc>
- <S5 Twilio gather required business info (campaign fields, rejection tips): https://www.twilio.com/docs/messaging/compliance/a2p-10dlc/collect-business-info>
- <S6 Twilio direct standard / low-volume standard onboarding (timelines): https://www.twilio.com/docs/messaging/compliance/a2p-10dlc/direct-standard-onboarding>
- <S7 Twilio direct sole proprietor overview: https://www.twilio.com/docs/messaging/compliance/a2p-10dlc/direct-sole-proprietor-registration-overview>
- <S8 Twilio ISV onboarding overview (brand per customer vs platform brand): https://www.twilio.com/docs/messaging/compliance/a2p-10dlc/onboarding-isv>
- <S9 Twilio toll-free verification API onboarding (3 business days, BRN): https://www.twilio.com/docs/messaging/compliance/toll-free/api-onboarding>
- <S10 Twilio changelog, unverified US toll-free blocked 2023-11-08: https://www.twilio.com/en-us/changelog/messaging-on-unverified-u-s--toll-free-phone-numbers-now-fully-b>
- <S11 Twilio Developer Guide to Toll-Free Verification (pending caps): https://www.twilio.com/en-us/blog/developer-guide-to-toll-free-verification>
- <S12 Twilio toll-free numbers product page: https://www.twilio.com/en-us/phone-numbers/toll-free>
- <S13 Twilio changelog, unregistered 10DLC blocked 2023-09-01 (error 30034): https://www.twilio.com/en-us/changelog/-u-s--a2p-10dlc--full-blocking-of-traffic-sent-from-unregistered>
- <S14 Twilio Messaging Policy: https://www.twilio.com/en-us/legal/messaging-policy>
- <S15 Twilio Advanced Opt-Out (STOP/START/HELP): https://www.twilio.com/docs/messaging/tutorials/advanced-opt-out>
- <S16 Twilio Verify developer best practices: https://www.twilio.com/docs/verify/developer-best-practices>
- <S17 Twilio Verify pricing: https://www.twilio.com/en-us/user-authentication-identity/pricing/verify>
- <S18 Twilio Lookup pricing: https://www.twilio.com/en-us/user-authentication-identity/pricing/lookup>

### Web push

- <https://www.w3.org/TR/push-api/ (W3C Push API, Working Draft 2025-12-01)>
- <https://www.rfc-editor.org/rfc/rfc8030 (HTTP Web Push: TTL, Urgency, Topic, 4096-byte minimum, 404 on expired subscription, 429/413)>
- <https://www.rfc-editor.org/rfc/rfc8291 (Web Push message encryption, 3993-byte plaintext max)>
- <https://www.rfc-editor.org/rfc/rfc8292 (VAPID)>
- <https://developer.mozilla.org/en-US/docs/Web/API/Push_API>
- <https://developer.mozilla.org/en-US/docs/Web/API/Push_API/Best_practices>
- <https://developer.mozilla.org/en-US/docs/Web/API/PushManager/subscribe>
- <https://developer.mozilla.org/en-US/docs/Web/API/PushSubscription>
- <https://developer.mozilla.org/en-US/docs/Web/API/ServiceWorkerGlobalScope/pushsubscriptionchange_event>
- <https://developer.mozilla.org/en-US/docs/Web/API/Notification/permission_static>
- <https://developer.mozilla.org/en-US/docs/Web/API/Notification/actions>
- <https://developer.mozilla.org/en-US/docs/Web/API/Service_Worker_API/Using_Service_Workers>
- <https://unpkg.com/@mdn/browser-compat-data@latest/data.json (MDN browser-compat-data v8.1.3)>
- <https://developer.apple.com/documentation/usernotifications/sending-web-push-notifications-in-web-apps-and-browsers (read via developer.apple.com/tutorials/data/documentation/usernotifications/sending-web-push-notifications-in-web-apps-and-browsers.json)>
