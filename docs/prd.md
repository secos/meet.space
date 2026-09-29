# Community Groups Platform — Product Requirements

**Companion documents:** `development-plan.md` (implementation), `workflows.md` (screen flows and sequence diagrams), `design.md` (visual design).

---

## 1. Product Overview

### Vision

A lightweight platform for hosting community groups and managing meetings. Organizers create groups and schedule meetings; community members join, RSVP, and participate with as little friction as possible. Paid groups can run fully white-labeled on their own domain.

### Design Principle

**Minimal interaction for community members.** Every user-facing flow should require as few clicks and as little typing as possible. No passwords. No onboarding wizards. No mandatory profile setup.

### V1 Scope

Any logged-in user can create groups and becomes the owner of the groups they create. Self-service group creation is open from day one.

---

## 2. User Roles

| Role | Scope | Can do |
|------|-------|--------|
| **Owner** | Per group | Everything a co-organizer can, plus billing, branding, custom domain, promoting/demoting/inviting co-organizers, removing co-organizers, transferring ownership, and deleting the group. Exactly one per group. |
| **Co-organizer** | Per group | Edit group info and community settings, manage meetings, view RSVPs, mark attendance, send custom messages, view analytics, remove regular members, delete any post in the group. |
| **Member** | Per group | RSVP, comment on meetings, reply to feed posts, create feed posts (if the group allows it), delete their own posts, leave the group, manage their notification preferences. |
| **Anonymous visitor** | Global | Browse, RSVP with an email (name optional), cancel that RSVP via the link in the confirmation email. |
| **Platform admin** | Global | You. Suspend and restore groups, delete any post. Set by a database flag, not through the UI. |

A user can be an owner of one group, a co-organizer of another, and a member of a third. There is no separate organizer account type.

**Accounts:** one account per email address. The account is created the first time the person clicks a magic link. Members set a display name on their profile page; email addresses are never shown publicly. Organizers see member and RSVP emails only for their own groups.

---

## 3. Authentication

The platform asks for the minimum identity each action needs.

| Action | Requirement |
|--------|-------------|
| Browse groups and meetings | None |
| RSVP to a meeting | Email only (name optional). No verification. |
| Cancel an RSVP | Link in the confirmation email, or logged in |
| Join a group | Magic link |
| Create a group | Logged in |
| Comment on a meeting, reply to a feed post | Logged in + group member |
| Create a feed post | Group member, if the group allows member posts (organizers always can) |
| Delete own post | Author |
| Delete any post in a group | Owner or co-organizer of that group; platform admin |
| Manage meetings, attendance, members, group info, messages, analytics | Owner or co-organizer |
| Billing, branding, custom domain, co-organizers, transfer ownership, delete group | Owner |
| Profile, notification preferences | Logged in |
| Suspend or restore a group | Platform admin |

**Rules:**

- **No passwords.** Authentication is entirely magic-link based. Links expire after 15 minutes and work once.
- **Link scanner protection.** Clicking a magic link opens a confirmation page with a "Log in" button. The token is only consumed when that button is pressed, so corporate email scanners that prefetch links can't use it up.
- **Accounts are created on first verification,** not when a link is requested.
- **Sessions are per domain.** Logging in on a group's custom domain creates a session for that domain only. It is the same account; the person just logs in separately on each domain. Cross-domain single sign-on is deferred (see §9).
- **Return to context.** After login the user returns to the page they were on. Only paths on the same domain are accepted as return destinations.
- **Anonymous RSVPs** are stored by email (lowercased). When that email is later verified, the RSVPs are linked to the account.

---

## 4. User Flows

Detailed screen flows and sequence diagrams for each of these are in `workflows.md`.

### 4.1 Organizer Flows

**Create a group.** Name, description, topic, location (a city chosen from Google Places suggestions, which supplies coordinates), timezone (defaulted from the location), and an optional cover image. Community settings: whether members can create feed posts (default on) and who receives meeting reminders (default: people who RSVP'd). The group is live in the directory immediately. If the user isn't logged in, they log in and land back on the form.

**Schedule a meeting.** Title, date and time, duration, type (in-person or virtual), description. The timezone defaults to the group's. In-person meetings use Google Places autocomplete for the address and show a map preview. Virtual meetings take a meeting link, which is **private**: it is shown only to people who have RSVP'd, on the meeting page after RSVPing and in their confirmation and reminder emails. Members who opted in receive a "new meeting" email.

**Edit, reschedule, or cancel a meeting.** Changes to date, time, location, or link, and cancellations, notify people who RSVP'd plus members who opted in to change notices. Rescheduling resets the reminder so it is sent again relative to the new time.

**Manage members and RSVPs.** Organizers see member lists and per-meeting RSVP lists. Co-organizers can remove regular members. The owner can remove anyone except themselves.

**Co-organizers.** The owner can promote a member, demote a co-organizer, or invite someone by email. An invite always has to be accepted through the emailed link, even if the person is already a member.

**Transfer ownership.** The owner picks a member or co-organizer; the previous owner becomes a co-organizer. For a paid group, the current subscription runs to the end of its period and won't renew; the new owner is prompted to subscribe (the person paying is always the person who entered the card).

**Moderate.** Owners and co-organizers can delete any post, reply, or comment in their group.

**Mark attendance.** After a meeting, organizers tick off who actually attended from the RSVP list. This feeds the analytics.

**Send a custom message.** Organizers can email the group's members (respecting preferences). Limited to 3 per group per day.

**View analytics.** Tables (no charts) of RSVPs per meeting, member growth, attendance rate, most active members, and meeting frequency.

**Delete a group (owner).** Cancels any subscription, removes the custom domain from the CDN, deletes uploaded files, and deletes the group's data.

### 4.2 Member and Visitor Flows

**Discover groups.** The directory sorts groups by distance from the visitor. Location comes from the browser's geolocation prompt; if that is denied or unavailable, the visitor is asked for their city. Coordinates are rounded to roughly city level. Custom domains don't show the directory.

**RSVP.** Enter email (and optionally a name), click RSVP, done: no verification, no account, no redirect. The browser remembers the RSVP on that domain so the page shows "You're going" on return visits, along with the virtual meeting link if there is one. Logged-in members RSVP with one click. RSVPing doesn't require joining the group. The confirmation email shows the time in the meeting's timezone, the virtual link if applicable, and a "Can't make it" link.

**Cancel an RSVP.** One click when logged in, or through the confirmation email's link (which opens a confirm page).

**Join and leave a group.** Joining needs a magic link if not logged in, or one click if logged in. Members can leave at any time; an owner must transfer ownership first.

**Post, comment, and reply.** Members can comment on meetings and reply to feed posts, and can create feed posts if the group allows it. Posts can include images and documents (PDF, DOC, DOCX; 10 MB per file, counted toward the group's storage). Members can delete their own posts.

**Profile.** Members set a display name and can delete their account. An account that owns groups must transfer or delete them first.

**Notification preferences.** Per group, members toggle four email types: new meeting announced, meeting reminder, meeting canceled or changed, and custom messages from organizers. All default to on. Every notification email has a one-click unsubscribe, including emails sent to anonymous RSVPers.

### 4.3 Custom Domain Experience (Paid Groups)

A paid group on its own domain is **fully white-label**. Visitors never see the platform's name, logo, links, or directory. The one v1 exception is the email sender address (below).

- The custom domain serves the group page, its meetings, login, member settings, and the organizer admin for **that group only**.
- Anything outside the group (other groups, the directory, a cross-group dashboard) is not available there.
- Login happens entirely on the custom domain: the login page, the magic link in the email, and the resulting session.
- Emails about the group link to the custom domain.
- **Email sender (v1):** all emails, including a white-label group's, are sent from the platform's one shared sending domain. The sender's display name is the group's name, and the email content is white-label, but the address itself reveals the shared domain. Per-group sending domains are deferred (see §9).

---

## 5. Pricing

**Organizers pay, per group. Members never pay.** Numbers are adjustable.

| | Free | Paid ($20/year per group) |
|---|---|---|
| Members | 150 (Dunbar's number) | Unlimited |
| File storage | 2 GB | 25 GB |
| Branding (colors, logo) | — | ✓ |
| Custom domain, fully white-label | — | ✓ |
| Everything else | ✓ | ✓ |

**Payments:** Stripe Checkout, yearly billing. Owners manage the card and cancellation through Stripe's customer portal.

**When a paid subscription ends:** branding reverts to the default, the custom domain is removed, uploads are blocked while storage is over 2 GB (existing files stay), and new joins are blocked while membership is over 150 (existing members stay).

---

## 6. Architecture & Infrastructure

### Tech Stack

| Layer | Technology |
|-------|-----------|
| **Application** | Go (standard library + minimal dependencies) |
| **Frontend** | Server-rendered HTML, HTMX, Tailwind CSS v4, PWA (manifest, service worker, offline page) |
| **Maps** | Google Maps JavaScript API with the Place Autocomplete element (Places API New) for address and city entry; Maps Embed API for map previews |
| **Database** | PostgreSQL, self-managed on the same host |
| **Object storage** | DigitalOcean Spaces (S3-compatible) for images and attachments |
| **Reverse proxy** | Caddy on the origin |
| **CDN** | BunnyCDN: caches anonymous reads, serves custom domains, provides custom-domain SSL |
| **Email** | Resend, paid plan (the free plan's 100 emails/day cap is too low for group announcements) |
| **Payments** | Stripe (Checkout, customer portal, webhooks) |
| **Background jobs** | Go goroutines: email queue, meeting reminders, cleanup of expired sessions and links |
| **Hosting** | Single DigitalOcean droplet, Dockerized |
| **Local dev** | Docker Compose |

### Domains and Request Flow

- The public domain (e.g. `yourcommunity.com`) is a CNAME to the Bunny pull zone, so all public traffic goes through the CDN.
- A separate origin hostname (e.g. `origin.yourcommunity.com`) points at the droplet, where Caddy terminates TLS. Bunny fetches from the origin.
- Custom domains are extra hostnames on the same pull zone.
- The Go app decides which "site" a request belongs to from the visitor's hostname: the main platform, or one specific group.

### CDN-First Caching

Anonymous page requests (home, directory, group pages, meeting pages) are served from the CDN cache. Anything personal is loaded after the page arrives through small HTMX fragments that are never cached: login state, RSVP status, virtual meeting links, comment forms, and admin UI.

**Rules:**

- Cached HTML never contains private data: no emails, no virtual meeting links.
- Every template separates the cacheable shell from dynamic fragments. This is structural, not an optimization for later.
- On every change (RSVP, comment, meeting edit, join), the app purges the affected pages on **every hostname they are served from**: the main domain and, if active, the group's custom domain.
- Directory URLs use rounded coordinates so nearby visitors share cache entries.

### Custom Domains

Activating a custom domain:

1. The owner enters the domain; the app registers it on the pull zone through Bunny's API.
2. The owner adds a CNAME record pointing to the pull zone.
3. The app checks that DNS resolves correctly.
4. The app requests Bunny's free SSL certificate for the hostname. This is a separate API step that only works once DNS is in place.
5. The domain goes live and appears in the app's list of known hostnames.

**Constraints:** root domains can't use a CNAME, so owners use a subdomain (e.g. `community.example.com`) or a DNS provider with CNAME flattening. A standard Bunny account supports 500 custom hostnames. Custom domains must also be added to the Google Maps browser key's allowed websites so address autocomplete keeps working in the organizer admin.

### Cost Estimate

| Service | Monthly Cost |
|---------|-------------|
| DigitalOcean droplet (2 GB RAM) | $12 |
| DigitalOcean Spaces (250 GB) | $5 |
| BunnyCDN (early traffic) | ~$1 |
| Resend (paid plan, 50k emails) | $20 |
| Google Maps Platform (Embed is free and unlimited; autocomplete within free monthly caps) | $0 |
| **Total** | **~$38/mo** |

This is above the original $10–30 target; email is the driver. If that matters, cheaper transactional email providers exist and can be swapped in behind the same email interface.

---

## 7. Data Model (Conceptual)

All primary keys are UUIDv7, generated in Go (time-ordered, so IDs sort by creation time). All emails are stored lowercased.

- **users**: email, display name, platform admin flag
- **sessions**: user, hostname the session belongs to, token hash, expiry
- **magic_links**: email, hostname, token hash, purpose (log in / join group / accept co-organizer invite), purpose details, return path, expiry, used time
- **groups**: slug, name, description, topic, location text and coordinates, timezone, cover image, member-posting setting, reminder audience setting, tier, member limit (none when paid), storage limit, custom domain and its status, branding, suspended time
- **memberships**: user, group, role (owner / co_organizer / member), joined time; exactly one owner per group, enforced by the database
- **meetings**: group, title, description, start time, timezone, duration, type, address with Place ID and coordinates, private virtual link, canceled flag, reminder-sent time
- **rsvps**: meeting, email, optional name, linked user (once verified), attended flag
- **posts**: group, meeting (for meeting comments), parent post (for replies to feed posts), author, content
- **attachments**: post, group (for storage totals), filename, type, storage key, size
- **notification_preferences**: user, group, four on/off toggles
- **email_opt_outs**: email and group, for anonymous RSVPers who unsubscribed
- **subscriptions**: group (one each), paying user, Stripe IDs, status, period end, cancel-at-period-end

---

## 8. Development Plan

See `development-plan.md` for the full phase-by-phase plan (13 phases, estimated 8–10 weeks solo).

---

## 9. Out of Scope

- **Recurring meeting schedules:** not needed.
- **Cross-domain single sign-on:** deferred. Revisit if many people use several branded groups.
- **Per-group email sending domains:** deferred. White-label groups send from the shared platform domain for now. Sending from each group's own domain would require the owner to add email DNS records, and each domain counts against Resend's plan limit.
- **Offline actions in the PWA:** cut. The PWA caches pages for reading and shows an offline page; RSVPs and posts need a connection.
- **IP-based location detection:** dropped in favor of browser location, then asking.
