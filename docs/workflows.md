# Community Groups Platform — User Workflows

Screen flows, sequence diagrams, and edge cases for every user-facing workflow.

**Companion documents:** `prd.md` (requirements), `development-plan.md` (implementation), `design.md` (visual design), `decisions.md` (decisions and open questions).

**Conventions used below:**

- **"Organizer"** means the owner or a co-organizer.
- **"Site"** is the hostname the visitor is on: the main platform or a group's custom domain. Unless noted, every flow works identically on both, and links in emails point back to the same site.
- Diagram participants: **CDN** (BunnyCDN), **App** (Go), **DB** (Postgres), **Mail** (email outbox → Amazon SES), **S3** (Amazon S3), **Stripe**.

---

## Table of Contents

1. [Discover Groups (Directory)](#1-discover-groups-directory)
2. [RSVP and Cancel an RSVP](#2-rsvp-and-cancel-an-rsvp)
3. [Log In (Magic Link)](#3-log-in-magic-link)
4. [Join and Leave a Group](#4-join-and-leave-a-group)
5. [Create a Group](#5-create-a-group)
6. [Schedule, Edit, and Cancel a Meeting](#6-schedule-edit-and-cancel-a-meeting)
7. [Posts, Replies, and Comments](#7-posts-replies-and-comments)
8. [Group Info and Owner Settings](#8-group-info-and-owner-settings)
9. [Co-organizers and Ownership Transfer](#9-co-organizers-and-ownership-transfer)
10. [Notification Preferences and Unsubscribe](#10-notification-preferences-and-unsubscribe)
11. [Attendance Marking](#11-attendance-marking)
12. [Analytics](#12-analytics)
13. [Custom Domain: Activation and Visitor Experience](#13-custom-domain-activation-and-visitor-experience)
14. [Profile and Account Deletion](#14-profile-and-account-deletion)
15. [Moderation and Platform Admin](#15-moderation-and-platform-admin)

---

## 1. Discover Groups (Directory)

Main platform only. Custom domains don't have a directory.

### Screen Flow

**Step 1: Landing page (`/`).** Hero with a search box and a "Groups near you" section.

**Step 2: Location.** The page's script checks for a location saved from a previous visit. If there isn't one:

- The browser asks for permission to share location.
- **Allowed:** coordinates are rounded to one decimal place (about 11 km) and saved in the browser.
- **Denied or unavailable:** a "Where are you?" field appears with city suggestions from Google Places. The chosen city's rounded coordinates are saved.
- A "Change location" link is always available.

**Step 3: Directory (`/directory?lat=39.7&lng=-105.0&q=hiking`).** Group cards, nearest first: name, topic, city, distance ("4 miles away"), member count, short description. Groups with no location (virtual-only) appear after located ones. Search box, topic filter, pagination.

**Step 4: Group page.** Clicking a card opens `/groups/{slug}`.

**Alternate entry:** an organizer shares a group or meeting link directly; the visitor lands on that page.

### Sequence

```
User → Browser: Visit /
Browser → CDN → (App on cache miss): GET /                   → Landing page
Browser: location.js → saved location? → else browser geolocation prompt
  [allowed]  Browser: round to 1 decimal, save
  [denied]   Browser: show city prompt (Place Autocomplete, cities) → round, save
User → Browser: Search or "Browse groups"
Browser → CDN: GET /directory?lat=39.7&lng=-105.0&q=hiking
  [cache hit]  CDN → Browser: cached page (shared by everyone at the same rounded location + query)
  [cache miss] CDN → App → DB: full-text match + haversine distance, NULL coords last, exclude suspended
               App → CDN → Browser: rendered page (cached 5 min)
Browser → App: GET /fragments/nav → login state
```

### Edge Cases

- **No results:** "No groups found for 'xyz' near you," with links to clear the search or browse all groups.
- **No groups at all:** invitation to create the first group (logged out → login → create form).
- **Suspended groups:** excluded from results.
- **Visitor moves:** "Change location" clears the saved location and repeats Step 2.

---

## 2. RSVP and Cancel an RSVP

The highest-priority flow for minimal friction.

### Screen Flow

**Scenario A: Anonymous visitor**

1. Visitor opens a meeting page. It shows the title, time in the meeting's timezone (e.g. "Sat, Oct 3 · 7:00 PM CDT"), duration, type, and address with map (in-person) or "Online meeting; the link is shared with people who RSVP" (virtual). It also shows the description and RSVP count.
2. The RSVP section shows **email** (required), **name (optional)**, and an **RSVP** button.
3. Visitor submits. The section changes in place to **"You're going!"** plus, for virtual meetings, the join link, and a **"Can't make it"** button. The count increases.
4. A confirmation email arrives: meeting details in the meeting's timezone, the join link if virtual, and a "Can't make it" link.
5. On later visits from the same browser, the page shows "You're going" (remembered by a signed cookie on this site).

**Scenario B: Logged-in user.** Same page, but the RSVP section is a single **RSVP** button; the account's email is used.

**Scenario C: Already going.** "You're going!" with the join link (virtual) and "Can't make it."

**Canceling:**

- **From the page:** "Can't make it" → the section returns to the RSVP form; count decreases.
- **From the email:** "Can't make it" link → a confirm page ("Cancel your RSVP for {meeting}?") → **Cancel RSVP** → "Your RSVP is canceled."

### Sequence

```
Browser → CDN: GET /groups/{slug}/meetings/{id}
CDN → Browser: cached shell (no virtual link, no emails)
Browser → App: GET /fragments/meetings/{id}/rsvp
App: session? RSVP cookie? → neither → email + name form

User → Browser: email, name (optional), RSVP
Browser → App: POST /groups/{slug}/meetings/{id}/rsvp
App: validate + lowercase email; rate-limit by client IP
App → DB: INSERT rsvp (id=UUIDv7) ON CONFLICT (meeting_id, email) DO NOTHING
App → Mail (outbox row, same transaction): confirmation with times in meeting tz, virtual link, signed cancel link (BaseURL of group); no typed name echoed; skipped if the address is suppressed (response is identical either way)
App → CDN: purge meeting page on every hostname the group is served from
App → Browser: Set-Cookie (signed, host-only RSVP list) + "You're going" fragment + HX-Trigger: rsvp-changed
Browser → App: GET /fragments/meetings/{id}/rsvp-count → updated count

-- Cancel from email --
User → Browser: click "Can't make it" in email
Browser → App: GET /rsvp/cancel/{token} → confirm page
User → Browser: Cancel RSVP
Browser → App: POST /rsvp/cancel/{token}
App: verify signature → DELETE rsvp → purge meeting page → "Your RSVP is canceled"
```

### Edge Cases

- **Duplicate RSVP:** treated as success ("You're going!"). No error, no second email.
- **Invalid email:** inline error in the form.
- **Past or canceled meeting:** no form; "This meeting has already happened" or "This meeting has been canceled."
- **Different browser:** the anonymous visitor sees the form again. Re-submitting the same email is a harmless duplicate.
- **Cancel link used twice:** "This RSVP was already canceled."
- **Later verification:** when the email is verified (any magic link), RSVPs with that email link to the account; the RSVP name becomes the display name if none is set.
- **Emails in the page:** never. The organizer's RSVP list is the only place RSVP emails appear.

---

## 3. Log In (Magic Link)

Works on the main platform and on custom domains. The session belongs to the site where the login happened.

### Screen Flow

1. **Trigger.** Clicking "Log in," or any action that needs login (create group, comment, etc.), goes to `/auth/login?redirect=/the/page`.
2. **Email.** One field: email. **Send login link.**
3. **Check your email.** "We sent a login link to {email}. It expires in 15 minutes." Plus a **Resend** link (3 per 15 minutes).
4. **Email.** "Log in to {site name}" with a button. The link points to the **same site**.
5. **Confirm page.** Clicking opens a page with one button: **Log in as {email}**. (This extra step exists because email security scanners open links automatically; only the button press uses up the link.)
6. **Logged in.** The user returns to the page they started from, or the dashboard.

### Sequence

```
Browser → App: GET /auth/login?redirect=/admin/groups/new
App → Browser: email form (redirect path in hidden field)
User → Browser: email, Send
Browser → App: POST /auth/magic-link
App: host is PUBLIC_HOST or an active custom domain? redirect path relative (starts with a single "/")?
App → DB: INSERT magic_link (email, host, purpose='auth', redirect_path, expires +15 min)
App → Mail (queued): link https://{host}/auth/verify/{token}
App → Browser: "Check your email"

User → Browser: click link
Browser → App: GET /auth/verify/{token}
App → DB: token valid, unused, unexpired, host matches? (read only)
App → Browser: confirm page with "Log in" button
User → Browser: Log in
Browser → App: POST /auth/verify/{token}
App → DB (one transaction):
  mark token used
  find or create user by email
  link RSVPs by email; set display name from latest RSVP if empty
  run purpose (auth: nothing extra)
  create session (host bound)
App → Browser: Set-Cookie (host-only, HttpOnly, Secure, SameSite=Lax) + 302 to redirect path or /admin
```

### Edge Cases

- **Expired or used link:** "This link has expired or was already used," with a button to request a new one.
- **Link opened on another site:** e.g. a custom-domain link pasted into the main site. The host doesn't match; treated as invalid.
- **Already logged in on this site:** `/auth/login` redirects straight to the redirect path.
- **Logged in on the main site but not the custom domain:** the user logs in again on the custom domain (same account). Cross-domain single sign-on is deferred.
- **Bad redirect path** (absolute URL, `//evil.com`): ignored; the user goes to `/admin`.
- **Multiple links requested:** each works independently until used or expired.

---

## 4. Join and Leave a Group

### Screen Flow

**Join, not logged in**

1. Group page shows **Join this group** with an email field.
2. Submit → "Check your email to confirm."
3. Email: "Join {group name}." → confirm page → **Join**.
4. The user returns to the group page as a member, logged in, with default notification preferences (all on).

**Join, logged in:** one click on **Join this group** → "Member" badge.

**Leave:** "Member" badge menu → **Leave group** → confirm → the join button returns. Notification preferences are removed.

### Sequence

```
-- Not logged in --
Browser → App: GET /fragments/groups/{slug}/join → email + Join
User → Browser: email, Join
Browser → App: POST /groups/{slug}/join
App: member limit reached? → "This group is full"
App → DB: INSERT magic_link (purpose='join_group', metadata {group_id}, host, redirect_path=/groups/{slug})
App → Mail (queued): join link
App → Browser: "Check your email" fragment
... confirm page, POST /auth/verify/{token} (as Workflow 3), purpose join_group:
App → DB: re-check limit; INSERT membership (member) + notification_preferences
App → CDN: purge group page (all hostnames)
App → Browser: session cookie + 302 /groups/{slug}

-- Logged in --
Browser → App: POST /groups/{slug}/join
App → DB: check limit; INSERT membership + preferences (idempotent)
App → CDN: purge group page
App → Browser: "Member" badge

-- Leave --
Browser → App: POST /groups/{slug}/leave
App: owner? → refuse ("Transfer ownership first")
App → DB: DELETE membership + notification_preferences
App → CDN: purge group page
App → Browser: join button fragment
```

### Edge Cases

- **Member limit** (free: 150; paid: none): join fragment shows "This group is full." Checked again at verification in case the group filled up meanwhile.
- **Already a member:** idempotent; shows the badge (or the role badge for organizers).
- **Owner tries to leave:** blocked with a link to transfer ownership.
- **Removed member rejoins:** allowed (no ban list in v1).
- **Suspended group:** join is unavailable.

---

## 5. Create a Group

Main platform only. Any logged-in user.

### Screen Flow

1. **Create Group** (nav or dashboard) → `/admin/groups/new`. If not logged in: login (Workflow 3) with `redirect=/admin/groups/new`, landing back on the form.
2. **Form:**
   - **Name, description, topic** (required).
   - **Location:** city suggestions from Google Places, which fill in coordinates. Left empty for virtual-only groups.
   - **Timezone:** preselected from the chosen city (or the browser's timezone), editable.
   - **Cover image** (optional, max 5 MB, images only).
   - **Community settings:** "Members can create feed posts" (on); "Send meeting reminders to: people who RSVP'd / all members who opted in" (RSVP'd).
3. **Create Group** → the new group page. The creator is the owner, with default notification preferences. The group appears in the directory.

### Sequence

```
Browser → App: GET /admin/groups/new
  [no session] → 302 /auth/login?redirect=/admin/groups/new → Workflow 3 → back here
App → Browser: form (+ Maps JS with Place Autocomplete, cities only)
User → Browser: fill form, pick city (hidden: location, latitude, longitude), Create
Browser → App: POST /admin/groups (multipart)
App: validate; slug from name (≤60 chars, -2/-3 on collision)
  [cover image] App: check type/size → S3: upload groups/{group_id}/{uuidv7}.jpg
App → DB (one transaction): INSERT group (UUIDv7, timezone, settings); INSERT membership (owner); INSERT notification_preferences
App → Browser: 302 /groups/{slug}
```

### Edge Cases

- **Places script fails to load:** plain text city field; no coordinates, so the group sorts after located groups in the directory. The organizer can fix it later by editing.
- **Slug collision / long names:** handled by suffixing and truncation. The slug never changes afterward, even if the group is renamed, so shared links keep working.
- **Invalid cover image:** inline error; other fields are kept.

---

## 6. Schedule, Edit, and Cancel a Meeting

Organizers only. Works on the main platform and on the group's custom domain.

### Screen Flow

**Create**

1. **New Meeting** from the group admin → `/admin/groups/{slug}/meetings/new`.
2. **Form:**
   - **Title** and **date and time**: entered in the meeting's timezone, shown next to the field ("Times are in Central Time").
   - **Timezone:** defaults to the group's; changeable for meetings held elsewhere.
   - **Duration** (default 60 minutes).
   - **Type:** In person / Online.
     - **In person:** address field with Google Places suggestions; choosing one fills in hidden address, Place ID, and coordinates, and shows a map preview below.
     - **Online:** meeting link, private (shown only to people who RSVP). It can be left blank and added later.
   - **Description** (optional).
3. **Create Meeting** → the meeting page. Members with "new meeting" notifications on get an email.

**Edit:** same form, prefilled. If the date, time, address, or link changes, the organizer sees "Attendees will be notified of this change" before saving.

**Cancel:** **Cancel meeting** → confirm → the page shows "This meeting has been canceled" and hides the RSVP form; attendees are notified.

**Delete:** only offered for meetings with no RSVPs (otherwise cancel instead).

### Sequence

```
Browser → App: GET /admin/groups/{slug}/meetings/new  (organizer check)
User → Browser: fill form; pick address → hidden place_id, location, latitude, longitude
Browser → App: POST /admin/groups/{slug}/meetings
App: validate (future start, duration > 0); convert local time in meeting tz → UTC
App → DB: INSERT meeting (UUIDv7, timezone, virtual_url private)
App → CDN: purge group page (all hostnames)
App → DB: members with new_meeting = true
App → Mail (queued): "New meeting: {title}" (times in meeting tz; links use the group's BaseURL; no virtual link)
App → Browser: 302 meeting page

-- Edit with a material change --
Browser → App: POST /admin/groups/{slug}/meetings/{id}
App → DB: UPDATE; if start time changed → reminder_sent_at = NULL
App → Mail (queued): "Updated: {title}" → RSVP'd emails (minus opt-outs) + members with meeting_changed = true
   (virtual link included only for RSVP'd recipients)
App → CDN: purge meeting + group pages

-- Cancel --
Browser → App: POST /admin/groups/{slug}/meetings/{id}/cancel
App → DB: canceled = true
App → Mail (queued): "Canceled: {title}" → same recipients as a change
App → CDN: purge meeting + group pages
```

### Edge Cases

- **Start time in the past:** rejected with an inline error.
- **Places unavailable:** plain address field; no map.
- **Online meeting without a link yet:** allowed. The RSVP'd view says "The organizer will share the link before the meeting." Adding the link later counts as a change and notifies attendees.
- **Rescheduled after the reminder went out:** the reminder is sent again relative to the new time.
- **Non-organizer:** 403.

---

## 7. Posts, Replies, and Comments

### Screen Flow

**Viewing:** anyone can read the group feed (on the group page) and meeting comments (on the meeting page). Authors appear by display name ("Member" if none is set), never by email.

**Taking part (members):**

- **Feed post:** a composer at the top of the feed, if the group allows member posts (organizers always see it).
- **Reply:** a "Reply" link under each feed post opens a small form. Replies are one level deep.
- **Meeting comment:** a form under the meeting's comments.
- **Attachments:** each form has **Attach files**. Images show inline; documents (PDF, DOC, DOCX) show as download links. Max 10 MB each.
- **Delete:** authors see "Delete" on their own posts; organizers see it on every post in their group.

**Not a member:** the forms are replaced by "Join this group to take part" (with the join button), or "Log in" if logged out.

### Sequence

```
Browser → App: GET /fragments/groups/{slug}/feed
App → DB: feed posts (newest first) + replies + attachments; membership + members_can_post
App → Browser: posts + composer (if allowed) / join prompt

User → Browser: write post, attach image, Post
Browser → App: POST /groups/{slug}/posts (multipart)
App: member? members_can_post or organizer? content non-empty?
App: each file: sniffed type allowed? ≤10 MB?
App → DB: SUM(size_bytes) for the group + new files ≤ storage_limit_bytes?
App → S3: upload groups/{group_id}/{uuidv7}{ext}
App → DB (one transaction): INSERT post; INSERT attachments (with group_id, size)
App → CDN: purge group page (all hostnames)
App → Browser: new post fragment inserted at the top

-- Reply --
Browser → App: POST /groups/{slug}/posts/{post_id}/replies   (parent must be a feed post in this group)

-- Delete --
Browser → App: DELETE /posts/{id}
App: author, organizer of the post's group, or platform admin?
App → DB: storage keys for the post and its replies' attachments
App → S3: delete those objects
App → DB: DELETE post (cascades to replies and attachment rows)
App → CDN: purge the affected page
App → Browser: remove the post from the page
```

### Edge Cases

- **Text is required;** an attachment alone isn't enough.
- **Storage full:** "This group has reached its storage limit." Owners also see an upgrade link on free groups.
- **Wrong file type / too large:** inline error; the text is kept.
- **Member posting turned off:** members can still reply and comment; only organizers create feed posts.
- **Reply to a reply, or reply on a meeting comment:** not supported (rejected by the server and the database).
- **Removed from the group:** existing posts stay; the person can no longer post.

---

## 8. Group Info and Owner Settings

Two separate pages with different permissions.

### Screen Flow

**Edit group (organizers):** `/admin/groups/{slug}/edit`. Name, description, topic, location (city suggestions), timezone, cover image, "Members can create feed posts," and the reminder audience setting. Changes show on the group page right away.

**Owner settings (owner only):** `/admin/groups/{slug}/settings`, in sections:

- **Plan:** Free or Paid (renews/ends on date). Free groups: **Upgrade — $20/year**. Paid groups: **Manage billing** (Stripe's customer portal: card, invoices, cancel).
- **Look (every group):** a picker with four choices (Clean, Lively, Confetti, Poster), each previewed in the group's current color. Saving takes effect immediately.
- **Branding (paid):** primary color picker and logo upload, with a live preview in the chosen look. Free groups see these two fields locked, with an upgrade link.
- **Custom domain (paid):** see Workflow 13.
- **Danger zone:** **Transfer ownership** (Workflow 9) and **Delete group**.

### Sequence

```
-- Upgrade --
Browser → App: POST /admin/groups/{slug}/upgrade  (owner)
App → Stripe: create Checkout session ($20/yr price; metadata group_id, payer user_id; return URL on this site)
App → Browser: 302 to Stripe Checkout
User → Stripe: pay
Stripe → Browser: 302 back to settings → "Processing your upgrade…" (polls until the tier updates)
Stripe → App: POST /webhooks/stripe (checkout.session.completed)
App: verify signature; skip if event already processed
App → DB: INSERT subscription; groups: tier='paid', member_limit=NULL, storage_limit=25 GB
App → CDN: purge group pages

-- Lapse (customer.subscription.deleted, or cancel-at-period-end reached) --
Stripe → App: webhook
App → DB: tier='free', member_limit=150, storage_limit=2 GB, branding='{}' (look is kept)
App → Bunny: remove custom hostname (if any); clear domain columns; refresh site cache
App: remove the domain from the Google browser key's allowed websites
App → CDN: purge group pages

-- Branding --
Browser → App: POST /admin/groups/{slug}/look (look)   (owner, any tier)
App: look in the allowlist? → UPDATE groups.look → purge group pages

Browser → App: POST /admin/groups/{slug}/branding (color, logo)   (owner, paid)
App: color matches #RRGGBB? logo is an image ≤2 MB? compute text-on-brand and derived palette from the hex
App → S3: upload logo
App → DB: UPDATE branding
App → CDN: purge group pages (all hostnames)

-- Delete group --
Browser → App: DELETE /admin/groups/{slug}   (owner; confirm by typing the group name)
App → Stripe: cancel subscription (if any)
App → Bunny: remove custom hostname (if any)
App → S3: delete everything under groups/{group_id}/
App → DB: DELETE group (cascades)
App → CDN: purge group pages
App → Browser: 302 /admin
```

### Edge Cases

- **Co-organizer opens owner settings:** 403.
- **Payment fails at renewal:** status becomes past due; the owner is emailed by Stripe; the group keeps paid features until Stripe cancels the subscription.
- **Lapsed with more than 150 members:** existing members stay; new joins are blocked ("This group is full") until membership drops below 150 or the group resubscribes.
- **Lapsed with more than 2 GB of files:** existing files stay; new uploads are blocked.
- **Webhook arrives twice:** ignored the second time (idempotent by event ID).

---

## 9. Co-organizers and Ownership Transfer

On the members page (`/admin/groups/{slug}/members`), each row shows the display name, email (organizers only), role, and joined date, plus actions based on the viewer's role.

### Screen Flow

- **Promote (owner):** "Make co-organizer" on a member → confirm → role changes.
- **Demote (owner):** "Make regular member" on a co-organizer → confirm.
- **Invite by email (owner):** **Invite co-organizer** → email → "Invite sent." The recipient gets "You've been invited to help organize {group}" → confirm page → **Accept**. Nothing changes until they accept.
- **Remove:** co-organizers can remove regular members; the owner can remove anyone but themselves. Confirm dialog.
- **Transfer ownership (owner):** from owner settings, choose a member or co-organizer → type the group name to confirm. The new owner is emailed; the previous owner becomes a co-organizer. For a paid group: "Your subscription will run until {date} and won't renew. {New owner} will be asked to subscribe, and won't be charged until your period ends."

### Sequence

```
-- Invite --
Browser → App: POST /admin/groups/{slug}/invite-coorganizer (email)   (owner)
App: already a co-organizer? → "Already a co-organizer"
App → DB: INSERT magic_link (purpose='invite_coorganizer', metadata {group_id}, host, redirect_path=/admin/groups/{slug})
App → Mail (queued): invite (link on the group's site)
Recipient → confirm page → POST /auth/verify/{token}
App → DB (one transaction): find or create user; membership: INSERT as co_organizer or UPDATE role; preferences if new; session
App → Browser: 302 group admin

-- Transfer --
Browser → App: POST /admin/groups/{slug}/transfer-ownership (target user_id)   (owner)
App → DB (one transaction): owner → co_organizer; target → owner   (demote first: the one-owner index refuses a second owner, and the single transaction means there is never none)
  [paid] App → Stripe: set cancel_at_period_end = true (new owner's Checkout later uses trial_end = old period end)
App → Mail (queued): new owner notice (+ subscribe link if paid)
App → Browser: 302 members page (now shown with co-organizer permissions)
```

### Edge Cases

- **Invite to someone not yet a member:** they become a co-organizer (and a member) when they accept.
- **Invite link expired:** the owner re-sends it.
- **Owner demotes or removes themselves:** not offered; transfer ownership instead.
- **Co-organizer calls an owner-only action directly:** 403.
- **Target leaves the group before the transfer:** "That person is no longer a member."

---

## 10. Notification Preferences and Unsubscribe

### Screen Flow

**Preferences:** from the dashboard or group page → `/settings/notifications/{slug}`. Four switches, all on by default, each saved immediately with a small "Saved" note:

- New meeting announced
- Meeting reminder
- Meeting canceled or changed
- Messages from organizers

Whether a member gets a reminder for a meeting they didn't RSVP to depends on the group's reminder setting (Workflow 5); the switch lets them opt out either way.

**Unsubscribe from an email:** every notification email has an unsubscribe link in the footer, and supporting mail apps show a built-in "Unsubscribe" button. The footer link opens a confirm page ("Stop {type} emails from {group}?") → **Unsubscribe** → done, with a link to manage all preferences (logged-in users). The mail-app button unsubscribes immediately.

**Anonymous RSVPers** receive reminders and change notices for meetings they RSVP'd to. Their unsubscribe link stops all further notification emails from that group to that address. RSVP confirmations still send, because the person directly requested them.

### Sequence

```
-- Toggle --
Browser → App: POST /settings/notifications/{slug} (meeting_reminder=false)
App → DB: UPDATE notification_preferences (this user, this group)
App → Browser: toggle fragment + "Saved"

-- Unsubscribe link --
Browser → App: GET /unsubscribe/{token}
App: verify HMAC → (user, group, type) or (email, group)
App → Browser: confirm page
Browser → App: POST /unsubscribe/{token}
App → DB: set that preference false, or INSERT email_opt_outs (email, group)
App → Browser: "You're unsubscribed"

-- Mail-app one-click (List-Unsubscribe-Post) --
Mail app → App: POST /unsubscribe/{token}   (same handler, no confirm page)
```

### Edge Cases

- **Tampered or invalid token:** "This link isn't valid. Log in to manage your preferences."
- **Preferences row missing:** created with defaults on first access.
- **Not logged in:** unsubscribe still works. The signed token is enough; no login required.
- **Leaving the group:** preferences are deleted; no more group emails.

---

## 11. Attendance Marking

### Screen Flow

1. For past, non-canceled meetings, organizers see **Mark attendance** in the group admin.
2. The page lists every RSVP: display name (or RSVP name, or email), RSVP date, and an **Attended** checkbox (pre-checked if previously marked). "Select all" helper.
3. **Save** → back to the meeting admin with "Attendance saved."

### Sequence

```
Browser → App: GET /admin/groups/{slug}/meetings/{id}/attendance   (organizer; meeting past and not canceled)
App → DB: RSVPs + linked user names
App → Browser: checklist
Browser → App: POST .../attendance (attended_ids[])
App → DB (one transaction): attended=false for all RSVPs of the meeting; attended=true WHERE id IN (...) AND meeting_id = {id}
App → Browser: 302 + "Attendance saved"
```

### Edge Cases

- **Future or canceled meeting:** no button; direct access returns 400.
- **No RSVPs:** "No one RSVP'd for this meeting."
- **Re-saving:** overwrites previous marks.

---

## 12. Analytics

### Screen Flow

Organizers open **Analytics** (`/admin/groups/{slug}/analytics`). Four tables, no charts:

| Table | Columns |
|-------|---------|
| Meetings (last 20) | Meeting, date, RSVPs, attended, attendance rate |
| Member growth (last 12 months) | Month, new members, total members |
| Most active members (top 20) | Member, RSVPs, posts, meetings attended |
| Meeting frequency (last 12 months) | Month, meetings held |

Canceled meetings are excluded. Attendance rate shows "N/A" when a meeting had no RSVPs. Empty tables show "No data yet." Member growth counts current members only. The queries are in the development plan (Phase 7).

### Sequence

```
Browser → App: GET /admin/groups/{slug}/analytics   (organizer)
App → DB: four queries scoped to group_id
App → Browser: rendered tables (no-store)
```

---

## 13. Custom Domain: Activation and Visitor Experience

Paid groups only.

### Screen Flow: Activation (owner)

1. Owner settings → **Custom domain** → enter `community.example.com` → **Add domain**.
2. The page shows instructions: "At your DNS provider, create a CNAME record: `community` → `{zone}.b-cdn.net`." Status: **Waiting for DNS**. If a root domain was entered: "Root domains can't use a CNAME. Use a subdomain like `community.example.com`, or a DNS provider that supports CNAME flattening."
3. Owner adds the record, then clicks **Check DNS**.
   - Not resolving yet: "Not detected yet. DNS changes can take up to 48 hours."
   - Resolving: the platform requests the SSL certificate. Status: **Active**, with a link to the domain.
4. **Remove domain** reverses everything; the group stays available on the main platform.

### Screen Flow: Visitor on the custom domain

- `https://community.example.com/` shows the group page: the group's logo, name, and brand color, with no platform name, logo, or links anywhere (header, footer, emails, browser tab, installed-app name).
- Meetings, RSVP, feed, login, account and notification settings, and (for organizers) group admin all work on this domain.
- The directory, other groups, "Create Group," and platform admin don't exist here (404).
- **Login** happens entirely on this domain (Workflow 3); the magic link points back here. Being logged in on the main platform doesn't carry over.
- All emails about this group link to this domain and use the group's name as the sender name. The sender address itself is on the shared platform sending domain in v1.

### Sequence

```
-- Add --
Browser → App: POST /admin/groups/{slug}/domain (community.example.com)   (owner, paid)
App: valid hostname? not an IP? not our own domain? not claimed by another group?
App → Bunny: AddHostname
App → DB: custom_domain, status='pending_dns'
App → Browser: CNAME instructions

-- Verify --
Browser → App: POST /admin/groups/{slug}/domain/verify
App: DNS lookup → CNAME points to the pull zone?
  [no]  → "Not detected yet"
  [yes] App → Bunny: LoadFreeCertificate(host)
        App: add host to the Google browser key's allowed websites
        App → DB: status='active'; refresh site cache
        App → Browser: "Active"

-- Visitor --
Visitor → CDN: GET https://community.example.com/
CDN → App (cache miss): request with the visitor's hostname (mechanism fixed in the Phase 9 spike)
App: site context = group X (white-label) → render group page with branding, no platform chrome
App → CDN → Visitor: page (cached per hostname)
```

### Edge Cases

- **Certificate request fails:** status stays "Waiting for DNS" with "SSL setup failed, try again shortly"; the check can be retried.
- **Subscription lapses:** the domain is removed automatically (Workflow 8). Visitors to the old domain no longer reach the group.
- **Unknown hostname** (someone points a domain at the CDN without registering it): 404.
- **Owner changes the domain:** remove the old one, add the new one.

---

## 14. Profile and Account Deletion

### Screen Flow

- **Profile (`/account`):** display name (prefilled from the most recent RSVP name if the user never set one), email (read-only), **Save**.
- **Delete account:** confirm by typing "delete".
  - If the user owns any group: blocked, with "Transfer or delete these groups first" and links to each.
  - Otherwise: memberships, preferences, sessions, and posts are deleted; RSVPs stay as anonymous RSVPs (email only) so organizers' headcounts don't change; the user is logged out.

### Sequence

```
Browser → App: POST /account (name)
App → DB: UPDATE users SET name

Browser → App: POST /account/delete
App → DB: owns a group? → refuse with list
App → S3: delete the user's attachments
App → DB: DELETE user (cascades memberships, sessions, posts, preferences; RSVPs keep email, user_id → NULL)
App → CDN: purge pages that showed their posts
App → Browser: clear cookie, 302 /
```

### Edge Cases

- **Empty display name:** allowed; shown as "Member."
- **Logged in on several domains:** deleting the account ends every session on every domain.

---

## 15. Moderation and Platform Admin

### Screen Flow

**Group moderation (organizers):** "Delete" on any post, reply, or comment in their group, with a confirm dialog. Deleting a feed post also deletes its replies and attachments.

**Platform admin (you, main platform only):** `/platform` lists all groups (search, owner, tier, member count, status).

- **Suspend** → confirm with a reason (kept in logs). The group disappears from the directory; its pages (on every domain) show "This group is unavailable"; members can't RSVP, join, or post.
- **Restore** reverses it.
- Platform admins can also delete any post anywhere.

### Sequence

```
Browser → App: POST /platform/groups/{slug}/suspend   (platform admin)
App → DB: suspended_at = NOW()
App → CDN: purge group + meeting pages on every hostname
App: log (admin, group, reason)
App → Browser: updated list
```

### Edge Cases

- **Suspended paid group:** the subscription isn't touched automatically; refunds or cancellation are handled manually in Stripe.
- **Platform admin flag:** set directly in the database; there is no UI to grant it.
