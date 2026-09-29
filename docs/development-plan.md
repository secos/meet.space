# Community Groups Platform — Development Plan

This document turns the PRD into concrete implementation details: hosting topology, project structure, packages, database schema, routes, security rules, template patterns, deployment configuration, and a phase-by-phase build plan.

**Companion documents:** `prd.md` (requirements), `workflows.md` (screen flows and sequence diagrams), `design.md` (visual design). `design.md` is the source of truth for colors, typography, spacing, components, and layouts; Tailwind classes shown in this document are illustrative.

---

## Hosting & Domain Topology

```
Visitor ──HTTPS──▶ BunnyCDN pull zone ──HTTPS──▶ Caddy (origin.yourcommunity.com) ──▶ Go app ──▶ Postgres
   yourcommunity.com         (cache, custom-domain SSL)
   community.example.com
```

- `yourcommunity.com` (public): CNAME to the Bunny pull zone. Never points at the droplet.
- `origin.yourcommunity.com` (origin): A record to the droplet. Caddy gets its Let's Encrypt certificate for this name.
- Custom domains: CNAME to the pull zone, registered as hostnames on it.
- The Go app must know the visitor's hostname for every request. How Bunny passes it to the origin is verified in a Phase 9 spike before custom domains are built (see Phase 9).

### Site Context (Host Resolution)

The first middleware on every request resolves the visitor's hostname into a **site context**:

| Hostname | Context | Behavior |
|----------|---------|----------|
| `PUBLIC_HOST` | Platform | Full platform: directory, all groups, create group, cross-group dashboard, platform admin |
| An **active** custom domain | Group (that group only) | `/` renders the group page. Only that group's pages, login, account settings, and admin are served. Directory, other groups, group creation, and platform admin return 404. No platform branding or links. |
| Anything else | None | 404 (the health check is the only exception) |

Custom domain lookups use an in-memory map (hostname → group ID), loaded at startup and refreshed whenever a domain changes state.

A helper `BaseURL(group)` returns `https://{custom_domain}` for active custom domains, otherwise `PUBLIC_BASE_URL`. Every link in an email about a group is built with it.

---

## Go Project Structure

```
communitygroups/
├── cmd/server/main.go               # Config, DB, router, background workers; `server migrate up|down` subcommand
├── internal/
│   ├── config/config.go             # Env config
│   ├── database/db.go               # pgxpool setup
│   ├── site/site.go                 # Host → site context resolution, BaseURL helper, custom domain cache
│   ├── middleware/
│   │   ├── auth.go                  # Session loading (host-bound), RequireUser, RequireMember, RequireOrganizer, RequireOwner, RequirePlatformAdmin
│   │   ├── security.go              # Origin check on state-changing requests, client IP from CDN header
│   │   ├── cache.go                 # Cache-Control helpers
│   │   └── ratelimit.go             # Token buckets (magic links, RSVPs, custom messages)
│   ├── handler/
│   │   ├── home.go                  # Landing page, directory
│   │   ├── group.go                 # Group page, join, leave
│   │   ├── meeting.go               # Meeting page, RSVP, cancel RSVP
│   │   ├── posts.go                 # Feed posts, replies, meeting comments, uploads, delete
│   │   ├── auth.go                  # Login page, magic link request, verify (GET confirm / POST consume), logout
│   │   ├── account.go               # Profile, account deletion, notification preferences, unsubscribe
│   │   ├── admin.go                 # Group/meeting management, members, co-organizers, attendance, messages
│   │   ├── billing.go               # Upgrade, customer portal, branding, custom domain lifecycle
│   │   ├── analytics.go             # Analytics tables
│   │   ├── platform.go              # Platform admin (suspend/restore groups)
│   │   ├── fragment.go              # All HTMX fragments
│   │   ├── pwa.go                   # Per-host manifest.json, service worker, offline page
│   │   └── webhook.go               # Stripe webhooks
│   ├── model/                       # Types + queries: user.go, group.go, meeting.go, post.go, notification.go, subscription.go
│   ├── email/
│   │   ├── email.go                 # Resend client (sends List-Unsubscribe headers)
│   │   └── queue.go                 # Buffered channel + worker goroutines + retry
│   ├── storage/spaces.go            # Upload/delete in DigitalOcean Spaces
│   ├── cdn/bunny.go                 # Purge, custom hostnames, free certificates
│   ├── token/token.go               # HMAC-signed tokens (RSVP cancel, unsubscribe, RSVP cookie)
│   ├── jobs/                        # reminders.go, cleanup.go (tickers)
│   └── tmpl/render.go               # Template loading and rendering
├── migrations/001_initial_schema.{up,down}.sql
├── templates/
│   ├── layouts/base.html
│   ├── pages/                       # home, directory, group, meeting, login, verify_confirm, account, notification_prefs,
│   │                                # unsubscribe_confirm, rsvp_cancel_confirm, dashboard, analytics, attendance,
│   │                                # group_settings, platform, offline, terms, privacy, error
│   ├── fragments/                   # nav, join_button, rsvp, rsvp_count, comments, feed, post, post_form
│   └── emails/                      # magic_link, rsvp_confirmation, meeting_announced, meeting_reminder,
│                                    # meeting_changed, custom_message, coorganizer_invite
├── static/
│   ├── css/output.css               # Built by Tailwind CLI (not committed)
│   ├── js/htmx.min.js               # Vendored
│   ├── js/places.js                 # Place Autocomplete wiring for admin forms and the directory city prompt
│   ├── js/location.js               # Directory: browser geolocation → rounded coords → navigate
│   ├── js/sw.js                     # Service worker source
│   └── img/                         # Default icons
├── input.css                        # Tailwind v4 entry: @import "tailwindcss" + @theme tokens from design.md
├── Dockerfile, docker-compose.yml, docker-compose.prod.yml, Caddyfile, Makefile
├── design.md
└── go.mod, go.sum
```

---

## Package Choices

| Purpose | Choice | Notes |
|---------|--------|-------|
| HTTP routing | `net/http` (Go 1.22+ patterns) | `GET /groups/{slug}` style routing is built in |
| Postgres | `github.com/jackc/pgx/v5` | `pgxpool` for pooling |
| UUIDs | `github.com/google/uuid` | `uuid.NewV7()` for every primary key |
| Migrations | `github.com/golang-migrate/migrate/v4` | Run explicitly via `server migrate up`, never on app startup |
| Config | `github.com/caarlos0/env/v11` | Struct tags |
| Templates | `html/template` | Contextual escaping |
| Payments | `github.com/stripe/stripe-go` | Checkout, customer portal, webhook signature verification |
| Object storage | `github.com/aws/aws-sdk-go-v2` (s3 + config only) | Spaces is S3-compatible |
| Email | Resend REST API via `net/http` | No SDK needed |
| CDN | Bunny REST API via `net/http` | No SDK needed |
| Signed tokens | `crypto/hmac` + `crypto/sha256` | Keyed with `APP_SECRET` |
| Logging | `log/slog` | |
| CSS | Tailwind v4 standalone CLI | No Node.js; configuration lives in `input.css` via `@theme` |
| Maps (browser) | Maps JavaScript API, `places` library, **Place Autocomplete element** | The legacy `google.maps.places.Autocomplete` is unavailable to new customers |
| Maps (display) | Maps Embed API iframe | Free, unlimited |

**Google Maps keys (two, both in cached HTML, so restrict them):**

- **Browser key:** restricted to the Maps JavaScript API and Places API (New), and to allowed websites (the main domain plus every active custom domain; updated when a domain activates or is removed).
- **Embed key:** restricted to the Maps Embed API only. Since Embed usage is free, it doesn't need a website restriction, which lets map previews work on any custom domain without allowlist maintenance.

---

## Database Schema

### Migration 001

```sql
-- 001_initial_schema.up.sql
-- All primary keys are UUIDv7, generated in Go with uuid.NewV7().
-- UUIDv7 is time-ordered, so ORDER BY id is chronological.
-- All emails are stored lowercased (enforced by CHECK constraints).

CREATE TABLE users (
    id                UUID PRIMARY KEY,
    email             TEXT NOT NULL UNIQUE CHECK (email = lower(email)),
    name              TEXT NOT NULL DEFAULT '',          -- Display name; emails are never shown publicly
    is_platform_admin BOOLEAN NOT NULL DEFAULT FALSE,    -- Set manually in the database
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- Users are created when a magic link is verified, never when one is requested.

CREATE TABLE sessions (
    id          UUID PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    host        TEXT NOT NULL,                           -- Session is only valid on this hostname
    token_hash  TEXT NOT NULL UNIQUE,                    -- SHA-256 of the cookie value
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_sessions_expires ON sessions (expires_at);

CREATE TABLE magic_links (
    id            UUID PRIMARY KEY,
    email         TEXT NOT NULL CHECK (email = lower(email)),
    host          TEXT NOT NULL,                         -- Link must be verified on this hostname
    token_hash    TEXT NOT NULL UNIQUE,
    purpose       TEXT NOT NULL DEFAULT 'auth'
                  CHECK (purpose IN ('auth', 'join_group', 'invite_coorganizer')),
    metadata      JSONB,                                 -- e.g. {"group_id": "..."}
    redirect_path TEXT CHECK (redirect_path IS NULL
                              OR (redirect_path LIKE '/%' AND redirect_path NOT LIKE '//%')),
    expires_at    TIMESTAMPTZ NOT NULL,
    used_at       TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_magic_links_expires ON magic_links (expires_at);

CREATE TABLE groups (
    id                   UUID PRIMARY KEY,
    slug                 TEXT NOT NULL UNIQUE,           -- Fixed at creation; renaming a group keeps the slug
    name                 TEXT NOT NULL,
    description          TEXT NOT NULL DEFAULT '',
    topic                TEXT NOT NULL DEFAULT '',
    location             TEXT NOT NULL DEFAULT '',       -- City text; empty for virtual-only groups
    latitude             DOUBLE PRECISION,
    longitude            DOUBLE PRECISION,
    timezone             TEXT NOT NULL,                  -- IANA name, e.g. 'America/Chicago'
    cover_image_key      TEXT,                           -- Spaces key
    members_can_post     BOOLEAN NOT NULL DEFAULT TRUE,
    reminder_audience    TEXT NOT NULL DEFAULT 'rsvped'
                         CHECK (reminder_audience IN ('rsvped', 'all_members')),
    tier                 TEXT NOT NULL DEFAULT 'free' CHECK (tier IN ('free', 'paid')),
    member_limit         INT DEFAULT 150,                -- NULL = unlimited (paid)
    storage_limit_bytes  BIGINT NOT NULL DEFAULT 2147483648,  -- 2 GB free; 25 GB paid
    custom_domain        TEXT UNIQUE CHECK (custom_domain = lower(custom_domain)),
    custom_domain_status TEXT CHECK (custom_domain_status IN ('pending_dns', 'active')),
    branding             JSONB NOT NULL DEFAULT '{}',    -- {"primary_color": "#4f46e5", "logo_key": "..."}
    suspended_at         TIMESTAMPTZ,                    -- Set by platform admin
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    search_vector        TSVECTOR GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(name, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(topic, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(description, '')), 'B')
    ) STORED
);
CREATE INDEX idx_groups_search ON groups USING GIN (search_vector);
CREATE INDEX idx_groups_topic ON groups (topic);

CREATE TABLE memberships (
    id         UUID PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id   UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    role       TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'co_organizer', 'member')),
    joined_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, group_id)
);
CREATE INDEX idx_memberships_group ON memberships (group_id);
CREATE UNIQUE INDEX idx_one_owner_per_group ON memberships (group_id) WHERE role = 'owner';

CREATE TABLE meetings (
    id               UUID PRIMARY KEY,
    group_id         UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    title            TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    starts_at        TIMESTAMPTZ NOT NULL,
    timezone         TEXT NOT NULL,                      -- Defaults to the group's timezone
    duration_min     INT NOT NULL DEFAULT 60 CHECK (duration_min > 0),
    meeting_type     TEXT NOT NULL DEFAULT 'in_person' CHECK (meeting_type IN ('in_person', 'virtual')),
    location         TEXT NOT NULL DEFAULT '',           -- Formatted address (in-person)
    place_id         TEXT,                               -- Google Place ID (in-person)
    latitude         DOUBLE PRECISION,
    longitude        DOUBLE PRECISION,
    virtual_url      TEXT,                               -- PRIVATE: never rendered in cached HTML
    canceled         BOOLEAN NOT NULL DEFAULT FALSE,
    reminder_sent_at TIMESTAMPTZ,                        -- Reset to NULL when rescheduled
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_meetings_group_starts ON meetings (group_id, starts_at);
CREATE INDEX idx_meetings_reminder_due ON meetings (starts_at)
    WHERE NOT canceled AND reminder_sent_at IS NULL;

CREATE TABLE rsvps (
    id          UUID PRIMARY KEY,
    meeting_id  UUID NOT NULL REFERENCES meetings(id) ON DELETE CASCADE,
    email       TEXT NOT NULL CHECK (email = lower(email)),
    name        TEXT NOT NULL DEFAULT '',               -- Optional at RSVP time
    user_id     UUID REFERENCES users(id) ON DELETE SET NULL,  -- Linked when the email is verified
    attended    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (meeting_id, email)
);
CREATE INDEX idx_rsvps_email ON rsvps (email);
CREATE INDEX idx_rsvps_user ON rsvps (user_id);

-- Posts:
--   meeting_id NULL, parent_id NULL      → feed post
--   meeting_id NULL, parent_id NOT NULL  → reply to a feed post (one level deep)
--   meeting_id NOT NULL, parent_id NULL  → meeting comment
CREATE TABLE posts (
    id          UUID PRIMARY KEY,
    group_id    UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    meeting_id  UUID REFERENCES meetings(id) ON DELETE CASCADE,
    parent_id   UUID REFERENCES posts(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content     TEXT NOT NULL CHECK (length(content) > 0),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (meeting_id IS NULL OR parent_id IS NULL)
);
CREATE INDEX idx_posts_feed ON posts (group_id, id DESC) WHERE meeting_id IS NULL AND parent_id IS NULL;
CREATE INDEX idx_posts_replies ON posts (parent_id, id) WHERE parent_id IS NOT NULL;
CREATE INDEX idx_posts_meeting ON posts (meeting_id, id) WHERE meeting_id IS NOT NULL;
CREATE INDEX idx_posts_user ON posts (user_id);

-- Database cascades delete attachment rows but not the files in Spaces.
-- The app deletes Spaces objects first, then the rows (see Phase 4).
CREATE TABLE attachments (
    id            UUID PRIMARY KEY,
    post_id       UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    group_id      UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,  -- For storage totals
    filename      TEXT NOT NULL,
    content_type  TEXT NOT NULL,
    storage_key   TEXT NOT NULL,
    size_bytes    BIGINT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_attachments_post ON attachments (post_id);
CREATE INDEX idx_attachments_group ON attachments (group_id);

CREATE TABLE notification_preferences (
    id               UUID PRIMARY KEY,
    user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id         UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    new_meeting      BOOLEAN NOT NULL DEFAULT TRUE,
    meeting_reminder BOOLEAN NOT NULL DEFAULT TRUE,
    meeting_changed  BOOLEAN NOT NULL DEFAULT TRUE,
    custom_message   BOOLEAN NOT NULL DEFAULT TRUE,
    UNIQUE (user_id, group_id)
);

-- Anonymous RSVPers who unsubscribed from a group's notification emails.
CREATE TABLE email_opt_outs (
    email      TEXT NOT NULL CHECK (email = lower(email)),
    group_id   UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (email, group_id)
);

CREATE TABLE subscriptions (
    id                     UUID PRIMARY KEY,
    group_id               UUID NOT NULL UNIQUE REFERENCES groups(id) ON DELETE CASCADE,
    payer_user_id          UUID REFERENCES users(id) ON DELETE SET NULL,
    stripe_customer_id     TEXT NOT NULL,
    stripe_subscription_id TEXT NOT NULL UNIQUE,
    status                 TEXT NOT NULL CHECK (status IN ('active', 'past_due', 'canceled')),
    current_period_end     TIMESTAMPTZ NOT NULL,
    cancel_at_period_end   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

```sql
-- 001_initial_schema.down.sql
DROP TABLE IF EXISTS subscriptions, email_opt_outs, notification_preferences, attachments,
    posts, rsvps, meetings, memberships, groups, magic_links, sessions, users;
```

`updated_at` is set by the application in each UPDATE query (no triggers).

---

## Route Definitions

Every route runs through site-context resolution first (see Hosting & Domain Topology). Routes marked **platform only** return 404 on custom domains.

Route categories:
1. **Cacheable pages:** `Cache-Control: public, s-maxage=300`. Never contain private data.
2. **Fragments:** `Cache-Control: no-store, private`. Personalized HTMX partials.
3. **Actions:** POST/DELETE, never cached, purge affected pages on all hostnames after success.

### Cacheable Pages

```
GET /                                   → Platform: landing page. Custom domain: the group page.
GET /directory                          → Directory (platform only). Query: q, topic, lat, lng (rounded), page
GET /groups/{slug}                      → Group page (custom domain: only its own slug)
GET /groups/{slug}/meetings/{id}        → Meeting page (no virtual link, no emails)
GET /terms, GET /privacy                → Legal pages (unbranded on custom domains)
```

### Fragments

```
GET /fragments/nav                      → Login state, account links, "Create Group" (platform only)
GET /fragments/groups/{slug}/join       → Join button / "Member" badge / "Group is full"
GET /fragments/groups/{slug}/feed       → Feed posts with replies + post form (if allowed)
GET /fragments/meetings/{id}/rsvp       → RSVP form, or "You're going" + virtual link + cancel button
GET /fragments/meetings/{id}/rsvp-count → Count badge
GET /fragments/meetings/{id}/comments   → Comments + comment form (members only)
```

### Auth

```
GET  /auth/login?redirect=/path         → Email form (redirect path carried in a hidden field)
POST /auth/magic-link                   → Send magic link for the current hostname
GET  /auth/verify/{token}               → Confirmation page with a "Log in" button (does NOT consume the token)
POST /auth/verify/{token}               → Consume token, create user if new, run purpose, create session, redirect
POST /auth/logout                       → Destroy session
```

### RSVP

```
POST   /groups/{slug}/meetings/{id}/rsvp    → Anonymous: email + optional name. Logged in: session email.
DELETE /groups/{slug}/meetings/{id}/rsvp    → Cancel (logged in, or anonymous with the RSVP cookie)
GET    /rsvp/cancel/{token}                 → Confirm page from the email link
POST   /rsvp/cancel/{token}                 → Cancel
```

### Membership

```
POST /groups/{slug}/join                → Logged in: join now. Otherwise: send join magic link.
POST /groups/{slug}/leave               → Leave (owners must transfer ownership first)
```

### Posts

```
POST   /groups/{slug}/posts                     → Feed post (member if members_can_post; organizers always)
POST   /groups/{slug}/posts/{post_id}/replies   → Reply to a feed post (member)
POST   /groups/{slug}/meetings/{id}/comments    → Meeting comment (member)
DELETE /posts/{id}                              → Author, owner/co-organizer of the group, or platform admin
```

All three create routes accept multipart uploads (attachments).

### Account

```
GET  /account                           → Display name, delete account
POST /account                           → Update display name
POST /account/delete                    → Delete account (blocked while the user owns any group)
GET  /settings/notifications/{slug}     → Four toggles for a group
POST /settings/notifications/{slug}     → Update one toggle (HTMX)
GET  /unsubscribe/{token}               → Confirm page
POST /unsubscribe/{token}               → Unsubscribe (also the target of the List-Unsubscribe-Post header)
```

### Organizer Admin (owner or co-organizer unless marked)

```
GET    /admin                                        → Dashboard (custom domain: that group only)
GET    /admin/groups/new                             → Create form (any logged-in user; platform only)
POST   /admin/groups                                 → Create group (creator becomes owner; platform only)
GET    /admin/groups/{slug}/edit                     → Edit info + community settings
POST   /admin/groups/{slug}                          → Save info + community settings
DELETE /admin/groups/{slug}                          → Delete group (OWNER)

GET    /admin/groups/{slug}/meetings/new             → Create meeting form
POST   /admin/groups/{slug}/meetings                 → Create + announce
GET    /admin/groups/{slug}/meetings/{id}/edit       → Edit form
POST   /admin/groups/{slug}/meetings/{id}            → Save; notify if date/time/location/link changed
POST   /admin/groups/{slug}/meetings/{id}/cancel     → Cancel + notify
DELETE /admin/groups/{slug}/meetings/{id}            → Delete (only meetings with no RSVPs; otherwise cancel)
GET    /admin/groups/{slug}/meetings/{id}/rsvps      → RSVP list
GET    /admin/groups/{slug}/meetings/{id}/attendance → Attendance checklist (past meetings)
POST   /admin/groups/{slug}/meetings/{id}/attendance → Save attendance

GET    /admin/groups/{slug}/members                  → Members with roles
DELETE /admin/groups/{slug}/members/{user_id}        → Remove (co-organizer: members only; owner: anyone but self)
POST   /admin/groups/{slug}/members/{user_id}/promote → Member → co-organizer (OWNER)
POST   /admin/groups/{slug}/members/{user_id}/demote  → Co-organizer → member (OWNER)
POST   /admin/groups/{slug}/invite-coorganizer       → Email invite (OWNER)
POST   /admin/groups/{slug}/transfer-ownership       → Transfer (OWNER)

GET    /admin/groups/{slug}/notify                   → Compose custom message
POST   /admin/groups/{slug}/notify                   → Send (3 per group per day)
GET    /admin/groups/{slug}/analytics                → Analytics tables
```

### Owner Settings: Billing, Branding, Domain (OWNER)

```
GET    /admin/groups/{slug}/settings           → Plan, branding, custom domain, danger zone
POST   /admin/groups/{slug}/upgrade            → Create Stripe Checkout session, redirect
POST   /admin/groups/{slug}/billing-portal     → Stripe customer portal (card, cancel)
POST   /admin/groups/{slug}/branding           → Primary color + logo (paid)
POST   /admin/groups/{slug}/domain             → Register custom domain (paid)
POST   /admin/groups/{slug}/domain/verify      → Check DNS; if correct, activate SSL and go live
DELETE /admin/groups/{slug}/domain             → Remove custom domain
```

### Platform Admin (platform only)

```
GET  /platform                          → All groups, search, status
POST /platform/groups/{slug}/suspend    → Suspend (pages show "unavailable"; purge)
POST /platform/groups/{slug}/restore    → Restore
```

### Other

```
POST /webhooks/stripe                   → Stripe events (signature verified)
GET  /manifest.json                     → Per-host manifest (group name and icons on custom domains)
GET  /sw.js                             → Service worker (root scope, short cache)
GET  /offline                           → Offline fallback page
GET  /static/*                          → Fingerprinted assets, cached for a year
GET  /health                            → DB ping (reachable on the origin hostname)
```

---

## Security Rules

These apply everywhere and are checked in code review.

1. **Return paths:** only relative paths starting with a single `/` are accepted (also enforced by a CHECK constraint). Anything else falls back to `/admin`.
2. **Hostnames in emails:** links in emails are only built for `PUBLIC_HOST` or an **active** custom domain from the site cache, never from an unvalidated `Host` header.
3. **Magic links:** GET shows a confirmation page; only POST consumes the token. Tokens are single-use and expire after 15 minutes. The verifying request's hostname must match `magic_links.host`.
4. **Sessions:** host-only cookies (no `Domain` attribute), `HttpOnly`, `Secure`, `SameSite=Lax`, 30-day expiry. A session is only honored on the hostname stored in `sessions.host`.
5. **CSRF:** `SameSite=Lax` plus an Origin/Referer check on every POST/DELETE (the origin must equal the current hostname).
6. **Signed tokens:** RSVP-cancel links, unsubscribe links, and the anonymous RSVP cookie are HMAC-SHA256 signed with `APP_SECRET` and include an expiry where appropriate.
7. **Private data never in cacheable HTML:** no emails, no virtual meeting links. They only appear in `no-store` fragments and in emails.
8. **Client IP:** taken from the CDN's forwarded-for header only when the request comes from the origin proxy; used for rate limiting.
9. **User content:** rendered through `html/template` escaping; never rendered as raw HTML. Uploads are validated by sniffed content type, not just extension.
10. **Branding values:** colors must match `^#[0-9a-fA-F]{6}$` before being written into a `<style>` block.

---

## Template Patterns: Cached Shell + Dynamic Fragments

> Styling here is illustrative. Use `design.md` for real tokens and components.

### Tailwind v4 Entry (`input.css`)

```css
@import "tailwindcss";

/* Default theme tokens, taken from design.md */
@theme {
  --color-brand: #2563eb;
  --color-brand-contrast: #ffffff;
  --font-sans: "Inter", ui-sans-serif, system-ui, sans-serif;
}
```

Because `@theme` tokens are real CSS custom properties, paid-group branding is a one-line override in the page head (see below). Utilities like `bg-brand` and `text-brand-contrast` pick it up automatically. `design.md` should state which tokens are overridable.

### Base Layout (`templates/layouts/base.html`)

```html
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{block "title" .}}{{.SiteName}}{{end}}</title>
    <link rel="stylesheet" href="/static/css/output.css?v={{.AssetVersion}}">
    {{if .Branding.PrimaryColor}}
    <style>:root { --color-brand: {{.Branding.PrimaryColor}}; }</style>  {{/* validated hex only */}}
    {{end}}
    <link rel="manifest" href="/manifest.json">
    <meta name="theme-color" content="{{.ThemeColor}}">
    <link rel="apple-touch-icon" href="{{.IconURL}}">
    <script src="/static/js/htmx.min.js?v={{.AssetVersion}}"></script>
    <script>if ('serviceWorker' in navigator) navigator.serviceWorker.register('/sw.js');</script>
    {{block "head" .}}{{end}}
</head>
<body class="bg-gray-50 text-gray-900 min-h-screen flex flex-col">
    <nav class="bg-white border-b px-4 py-3">
        <div class="max-w-4xl mx-auto flex items-center justify-between">
            {{/* Platform: platform name. Custom domain: group logo/name only. */}}
            <a href="/" class="font-bold text-lg">{{.SiteName}}</a>
            <div hx-get="/fragments/nav" hx-trigger="load" hx-swap="innerHTML">
                <a href="/auth/login" class="text-sm">Log in</a>
            </div>
        </div>
    </nav>
    <main class="flex-1 max-w-4xl mx-auto w-full px-4 py-6">{{block "content" .}}{{end}}</main>
    <footer class="border-t px-4 py-4 text-center text-sm text-gray-500">
        {{/* Custom domains: group name + Terms/Privacy only. No platform name or links. */}}
        {{.FooterText}} · <a href="/terms">Terms</a> · <a href="/privacy">Privacy</a>
    </footer>
</body>
</html>
```

`SiteName`, `FooterText`, `IconURL`, `ThemeColor`, and `Branding` come from the site context, so the same templates serve both the platform and white-label domains.

### Meeting Page (`templates/pages/meeting.html`)

```html
{{define "content"}}
<article>
    <a href="/groups/{{.Group.Slug}}" class="text-brand text-sm">&larr; {{.Group.Name}}</a>
    <h1 class="text-2xl font-bold mb-1">{{.Meeting.Title}}</h1>

    {{if .Meeting.Canceled}}<p class="text-red-700 font-semibold">This meeting has been canceled.</p>{{end}}

    <p class="text-gray-600 text-sm mb-4">
        {{/* Always rendered in the meeting's timezone, with the zone abbreviation, e.g. "Sat, Oct 3 · 7:00 PM CDT" */}}
        <time datetime="{{.Meeting.StartsAtISO}}">{{.Meeting.StartsAtLocal}}</time>
        · {{.Meeting.DurationMin}} min · {{.Meeting.TypeLabel}}
    </p>

    {{if eq .Meeting.Type "in_person"}}
        <p class="mb-2">{{.Meeting.Location}}</p>
        {{if .Meeting.PlaceID}}
        <iframe class="w-full h-52 rounded border mb-4" loading="lazy"
                referrerpolicy="no-referrer-when-downgrade"
                src="https://www.google.com/maps/embed/v1/place?key={{.EmbedKey}}&q=place_id:{{.Meeting.PlaceID}}"></iframe>
        {{end}}
    {{else}}
        <p class="mb-4 text-sm text-gray-600">Online meeting. The link is shared with people who RSVP.</p>
    {{end}}

    {{if .Meeting.Description}}<div class="prose mb-6">{{.Meeting.Description}}</div>{{end}}

    <div hx-get="/fragments/meetings/{{.Meeting.ID}}/rsvp-count" hx-trigger="load, rsvp-changed from:body">
        {{.Meeting.RSVPCount}} going
    </div>

    <div id="rsvp-section" hx-get="/fragments/meetings/{{.Meeting.ID}}/rsvp" hx-trigger="load">
        <p class="text-sm text-gray-400">Loading…</p>
    </div>

    <section>
        <h2 class="text-lg font-semibold mb-3">Discussion</h2>
        <div hx-get="/fragments/meetings/{{.Meeting.ID}}/comments" hx-trigger="load">
            <p class="text-sm text-gray-400">Loading comments…</p>
        </div>
    </section>
</article>
{{end}}
```

### RSVP Fragment (`templates/fragments/rsvp.html`)

The RSVP handler responds with this fragment and an `HX-Trigger: rsvp-changed` header so the count refreshes.

```html
{{if .Going}}
    <div class="bg-green-50 border border-green-200 rounded px-4 py-3 text-sm">
        <p class="font-medium text-green-800">You're going!</p>
        {{if .VirtualURL}}<p class="mt-1">Join link: <a href="{{.VirtualURL}}" class="text-brand underline">{{.VirtualURL}}</a></p>{{end}}
        <button hx-delete="/groups/{{.GroupSlug}}/meetings/{{.MeetingID}}/rsvp" hx-target="#rsvp-section"
                class="mt-2 text-gray-600 underline">Can't make it</button>
    </div>
{{else if .Closed}}
    <p class="text-sm text-gray-600">{{.ClosedReason}}</p>  {{/* "already happened" or "canceled" */}}
{{else}}
    <form hx-post="/groups/{{.GroupSlug}}/meetings/{{.MeetingID}}/rsvp" hx-target="#rsvp-section"
          class="flex flex-col gap-2">
        {{if not .User}}
            <input type="email" name="email" placeholder="Your email" required class="border rounded px-3 py-2 text-sm">
            <input type="text" name="name" placeholder="Your name (optional)" class="border rounded px-3 py-2 text-sm">
        {{end}}
        {{/* Logged in: no email field. The server uses the session's email. */}}
        <button type="submit" class="bg-brand text-brand-contrast px-4 py-2 rounded text-sm font-medium">RSVP</button>
    </form>
{{end}}
```

### Place Autocomplete (admin forms and directory city prompt)

Loaded only on pages that need it (meeting form, group form, directory city prompt):

```html
{{define "head"}}
<script async src="https://maps.googleapis.com/maps/api/js?key={{.BrowserMapsKey}}&loading=async&libraries=places&callback=initPlaces"></script>
<script src="/static/js/places.js" defer></script>
{{end}}
```

`places.js` mounts a Place Autocomplete element in place of the plain text input. On selection it fetches the place's ID, formatted address, and location, and writes them to hidden inputs (`place_id`, `location`, `latitude`, `longitude`) plus a map preview iframe using the embed key. The group form and the directory prompt restrict suggestions to cities. If the script fails to load, the plain text input remains and the coordinate fields stay empty. Confirm the element's event names and field names against Google's current Place Autocomplete documentation when implementing.

---

## CDN Client (`internal/cdn/bunny.go`)

```go
type Bunny struct {
    apiKey     string
    pullZoneID string
    http       *http.Client
}

func (b *Bunny) do(ctx context.Context, method, url string, body io.Reader) error {
    req, err := http.NewRequestWithContext(ctx, method, url, body)
    if err != nil {
        return err
    }
    req.Header.Set("AccessKey", b.apiKey)
    if body != nil {
        req.Header.Set("Content-Type", "application/json")
    }
    resp, err := b.http.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode >= 300 {
        msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
        return fmt.Errorf("bunny %s %s: %d %s", method, url, resp.StatusCode, msg)
    }
    return nil
}

// PurgeURL purges one absolute URL.
func (b *Bunny) PurgeURL(ctx context.Context, u string) error {
    return b.do(ctx, "POST", "https://api.bunny.net/purge?url="+url.QueryEscape(u), nil)
}

// AddHostname registers a custom domain on the pull zone.
func (b *Bunny) AddHostname(ctx context.Context, host string) error {
    body, _ := json.Marshal(map[string]string{"Hostname": host})
    return b.do(ctx, "POST", fmt.Sprintf("https://api.bunny.net/pullzone/%s/addHostname", b.pullZoneID), bytes.NewReader(body))
}

// LoadFreeCertificate requests Bunny's free SSL certificate. Only succeeds once DNS points at the pull zone.
func (b *Bunny) LoadFreeCertificate(ctx context.Context, host string) error {
    return b.do(ctx, "GET", "https://api.bunny.net/pullzone/loadFreeCertificate?hostname="+url.QueryEscape(host), nil)
}

// RemoveHostname removes a custom domain from the pull zone.
func (b *Bunny) RemoveHostname(ctx context.Context, host string) error {
    body, _ := json.Marshal(map[string]string{"Hostname": host})
    return b.do(ctx, "DELETE", fmt.Sprintf("https://api.bunny.net/pullzone/%s/removeHostname", b.pullZoneID), bytes.NewReader(body))
}
```

Confirm exact endpoint paths and parameters for the hostname and certificate calls against Bunny's API reference when implementing.

**Purging across hostnames.** Handlers never build purge URLs themselves; they call one helper:

```go
// PurgePaths purges the given paths on every hostname the group is served from.
func (h *Handler) PurgePaths(ctx context.Context, g model.Group, paths ...string) {
    hosts := []string{h.cfg.PublicHost}
    if g.CustomDomain != "" && g.CustomDomainStatus == "active" {
        hosts = append(hosts, g.CustomDomain)
    }
    for _, host := range hosts {
        for _, p := range paths {
            if err := h.cdn.PurgeURL(ctx, "https://"+host+p); err != nil {
                slog.Error("cdn purge failed", "host", host, "path", p, "err", err)
            }
        }
    }
}
```

On the custom domain the group page is also served at `/`, so group-page purges include both `/groups/{slug}` and `/`.

| Change | Paths purged |
|--------|--------------|
| RSVP added or canceled | `/groups/{slug}/meetings/{id}` |
| Comment on a meeting | `/groups/{slug}/meetings/{id}` |
| Feed post or reply | `/groups/{slug}`, `/` |
| Join or leave | `/groups/{slug}`, `/` |
| Meeting created, edited, canceled | `/groups/{slug}`, `/`, `/groups/{slug}/meetings/{id}` |
| Group info or branding changed, suspended, restored | `/groups/{slug}`, `/` (plus directory pages on the main host) |

Directory pages expire on their 5-minute TTL rather than being purged (their URLs vary by location and search).

---
## Docker & Deployment Configuration

### Dockerfile

```dockerfile
# --- CSS: Tailwind v4 standalone CLI (no Node.js) ---
FROM alpine:3.20 AS css
ARG TAILWIND_VERSION=4.1.17
RUN apk add --no-cache curl \
 && curl -fsSL -o /usr/local/bin/tailwindcss \
    https://github.com/tailwindlabs/tailwindcss/releases/download/v${TAILWIND_VERSION}/tailwindcss-linux-x64-musl \
 && chmod +x /usr/local/bin/tailwindcss
WORKDIR /src
COPY input.css ./
COPY templates/ ./templates/
COPY static/js/ ./static/js/
RUN tailwindcss -i input.css -o /out/output.css --minify

# --- Go build ---
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /server ./cmd/server

# --- Runtime ---
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata   # tzdata is required for meeting timezones
WORKDIR /app
COPY --from=build /server .
COPY templates/ ./templates/
COPY static/ ./static/
COPY --from=css /out/output.css ./static/css/output.css
COPY migrations/ ./migrations/
EXPOSE 8080
CMD ["./server"]
```

Pin `TAILWIND_VERSION` and `golang` to the current releases when you start.

### docker-compose.yml (Local Development)

```yaml
services:
  app:
    build: .
    ports: ["8080:8080"]
    environment:
      DATABASE_URL: postgres://cg:devpassword@db:5432/cg?sslmode=disable
      PUBLIC_HOST: localhost:8080
      PUBLIC_BASE_URL: http://localhost:8080
      APP_SECRET: dev-only-change-me          # HMAC key for signed tokens
      RESEND_API_KEY: ""                      # Empty → emails are logged to stdout
      BUNNY_API_KEY: ""                       # Empty → CDN calls are no-ops
      BUNNY_PULL_ZONE_ID: ""
      SPACES_ENDPOINT: ""                     # Empty → uploads go to a local ./uploads dir
      SPACES_BUCKET: ""
      SPACES_KEY: ""
      SPACES_SECRET: ""
      STRIPE_SECRET_KEY: ""                   # Use Stripe test keys + `stripe listen` for webhooks
      STRIPE_WEBHOOK_SECRET: ""
      STRIPE_PRICE_ID: ""
      GOOGLE_MAPS_BROWSER_KEY: ""
      GOOGLE_MAPS_EMBED_KEY: ""
      ENV: development
    depends_on:
      db: { condition: service_healthy }
    volumes:
      - ./templates:/app/templates
      - ./static:/app/static

  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: cg
      POSTGRES_PASSWORD: devpassword
      POSTGRES_DB: cg
    ports: ["5432:5432"]
    volumes: [pgdata:/var/lib/postgresql/data]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U cg"]
      interval: 2s
      retries: 10

volumes:
  pgdata:
```

**Testing custom domains locally:** browsers resolve any `*.localhost` name to your machine, so seed a paid group with `custom_domain = 'hikers.localhost:8080'` and status `active`, then visit `http://hikers.localhost:8080` to exercise white-label routing and host-bound login.

### docker-compose.prod.yml

```yaml
services:
  app:
    image: communitygroups:latest
    restart: always
    env_file: .env                          # Same variables as dev, with production values
    depends_on:
      db: { condition: service_healthy }

  db:
    image: postgres:16-alpine
    restart: always
    environment:
      POSTGRES_USER: cg
      POSTGRES_PASSWORD: ${DB_PASSWORD}
      POSTGRES_DB: cg
    volumes: [pgdata:/var/lib/postgresql/data]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U cg"]
      interval: 10s
      retries: 5

  caddy:
    image: caddy:2-alpine
    restart: always
    ports: ["80:80", "443:443"]
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile
      - caddy_data:/data
      - caddy_config:/config
    depends_on: [app]

volumes:
  pgdata:
  caddy_data:
  caddy_config:
```

### Caddyfile (starting point; finalized in the Phase 9 spike)

```caddyfile
origin.yourcommunity.com {
    reverse_proxy app:8080
}
```

This works as-is if Bunny connects with the origin hostname and passes the visitor's hostname to the app some other way. If instead Bunny forwards the visitor's hostname as the `Host` header, Caddy also needs a block that accepts any hostname on the origin's certificate. The Phase 9 spike decides which.

### Makefile

```makefile
.PHONY: dev css migrate-up migrate-down build deploy

dev:
	docker compose up --build

css:            # Watch mode; download the standalone binary to ./bin once
	./bin/tailwindcss -i input.css -o static/css/output.css --watch

migrate-up:
	docker compose exec app ./server migrate up

migrate-down:
	docker compose exec app ./server migrate down

build:
	docker build -t communitygroups:latest .

deploy: build
	docker compose -f docker-compose.prod.yml up -d
	docker compose -f docker-compose.prod.yml exec app ./server migrate up
```

---

## Phase-by-Phase Implementation

**Estimated total: 8–10 weeks solo.** Phases 0–4 give you a working product for your own groups; the rest can overlap.

### Phase 0: Scaffolding (2–3 days)

1. `go mod init`; create the directory structure above.
2. `cmd/server/main.go`: load config, open the DB pool, register routes, start background workers, and handle the `migrate up|down` subcommand. Migrations never run automatically on startup.
3. `internal/config`: `DATABASE_URL`, `PUBLIC_HOST`, `PUBLIC_BASE_URL`, `APP_SECRET`, Resend, Bunny, Spaces, Stripe, both Google keys, `ENV`.
4. Docker Compose; migration 001.
5. Tailwind v4 standalone CLI; `input.css` with `@theme` tokens taken from `design.md`.
6. Vendor `htmx.min.js`; `tmpl/render.go` with layout composition and site-context template data (`SiteName`, `FooterText`, `Branding`, etc.).
7. `site/site.go`: host → site context middleware, custom-domain cache, `BaseURL(group)`.
8. `middleware/security.go`: Origin check on POST/DELETE; client IP extraction.
9. `token/token.go`: HMAC sign/verify with expiry.
10. Base layout, a placeholder home page, and `/health`.
11. Verify: `make dev` → styled page at `localhost:8080`, and `hikers.localhost:8080` resolves to a group context.

### Phase 1: Groups & Meetings (3–4 days)

Group creation needs a logged-in user. Build a minimal magic-link login first (request → email logged to stdout → verify → session) and finish it in Phase 3.

1. `model/group.go`: CRUD. Creating a group inserts an `owner` membership and default notification preferences in the same transaction. The slug is generated from the name (lowercase, hyphenated, max 60 chars, `-2`/`-3` suffix on collision) and never changes afterward.
2. Group form: name, description, topic, location via Place Autocomplete restricted to cities (fills `location`, `latitude`, `longitude`), timezone (select, defaulted from the chosen city; editable), cover image, `members_can_post`, `reminder_audience`.
3. `model/meeting.go`: CRUD. `timezone` defaults to the group's. The form's local date and time are interpreted in the meeting's timezone and stored as UTC.
4. Meeting form: in-person → Place Autocomplete (fills `location`, `place_id`, `latitude`, `longitude`) + map preview; virtual → `virtual_url`.
5. `RequireUser`, `RequireOrganizer(slug)`, `RequireOwner(slug)` middleware; permissions follow the route table.
6. Public group and meeting pages. Times render in the meeting's timezone with the zone abbreviation. `virtual_url` is never passed to public page templates.
7. Dashboard: on the platform, groups the user owns, co-organizes, and belongs to; on a custom domain, only that group.
8. Styling per `design.md`.

### Phase 2: RSVP (2–3 days)

1. `CreateRSVP`: lowercase the email; `INSERT … ON CONFLICT (meeting_id, email) DO NOTHING` (a duplicate is treated as success).
2. Logged in: the email comes from the session, never from the form. Anonymous: email (required) + name (optional).
3. Anonymous RSVPs set a signed, host-only cookie listing the visitor's RSVP IDs, so the RSVP fragment can show "You're going" and the virtual link on return visits.
4. RSVP fragment states: form / going (with virtual link and "Can't make it") / closed (past or canceled).
5. Cancel: `DELETE` for logged-in users or cookie holders; signed `/rsvp/cancel/{token}` links for email (GET confirm page, POST cancel).
6. `email/queue.go`: buffered channel + workers + retry with backoff. RSVP confirmation email: time in the meeting's timezone, virtual link (if any), "Can't make it" link, all links built with `BaseURL(group)`.
7. Organizer RSVP list (name, or email if no name).
8. Rate limit: 10 RSVPs per client IP per minute.
9. CDN purge calls (no-ops until Phase 9).

### Phase 3: Auth, Membership & Profile (4–5 days)

1. `POST /auth/magic-link`: validate that the current hostname is `PUBLIC_HOST` or an active custom domain; validate the redirect path; store `email`, `host`, `purpose`, `metadata`, `redirect_path`; email a link to `https://{host}/auth/verify/{token}`. Rate limit 3 per email per 15 minutes.
2. `GET /auth/verify/{token}`: render a confirmation page with a "Log in" button. Do not consume the token.
3. `POST /auth/verify/{token}`, in one transaction:
   - Check that the token is unused and unexpired and that its host matches the request host.
   - Mark it used.
   - Find or create the user.
   - Link RSVPs by email; if the user has no name yet, use the most recent RSVP name.
   - Run the purpose: `join_group` creates a membership and default preferences; `invite_coorganizer` creates or upgrades the membership to `co_organizer`.
   - Create a session bound to the host.
   - Set the cookie and redirect.
4. Session middleware: load a session only if `sessions.host` equals the request host.
5. Join: logged in → one click; otherwise email → `join_group` magic link returning to the group page. Enforce `member_limit` (NULL = unlimited); the join fragment shows "This group is full" at the limit.
6. Leave: removes the membership and preferences. An owner must transfer ownership first.
7. Account page: display name; delete account (blocked while the user owns a group; deletes their posts, un-links RSVPs).
8. Nav and join fragments.

### Phase 4: Posts, Replies, Uploads & Moderation (4–5 days)

1. Feed posts (members if `members_can_post`, organizers always), replies to feed posts (members, one level), meeting comments (members). Non-members see content and a "Join to take part" prompt.
2. Authors are shown by display name; members with no name show as "Member". Never show emails.
3. Uploads: images (JPEG, PNG, GIF, WebP) and documents (PDF, DOC, DOCX), max 10 MB each, validated by sniffed content type. Before storing, check `SUM(size_bytes)` for the group plus the new files against `storage_limit_bytes`. Keys: `groups/{group_id}/{uuidv7}{ext}`.
4. Files are public-read in Spaces (groups are public). Link to them directly via the Spaces CDN URL.
5. Delete (author, group organizer, platform admin): delete Spaces objects for the post and its replies' attachments first, then the rows.
6. HTMX: new posts are inserted without a page reload; purge affected pages.

### Phase 5: Co-organizers & Ownership (2–3 days)

1. Promote and demote (owner).
2. Invite by email (owner): always sends an `invite_coorganizer` magic link, even to existing members. Nothing changes until the recipient accepts.
3. Remove members: co-organizers can remove `member`s; the owner can remove anyone but themselves.
4. Transfer ownership (owner): one transaction swaps roles. If the group is paid, set the Stripe subscription to cancel at period end and email the new owner a link to subscribe.
5. Write a table-driven test covering every role × action in the route table.

### Phase 6: Email Notifications (3–4 days)

1. Resend paid plan. Every notification email carries `List-Unsubscribe` and `List-Unsubscribe-Post: List-Unsubscribe=One-Click` headers pointing at a signed `/unsubscribe/{token}` URL. The token identifies either (user, group, type) or, for anonymous RSVPers, (email, group).
2. Unsubscribe: GET shows a confirm page, POST applies it (to `notification_preferences` or `email_opt_outs`).
3. New meeting → members with `new_meeting = true`.
4. Changed or canceled → RSVP'd emails (minus opt-outs) plus members with `meeting_changed = true`. Changing the start time sets `reminder_sent_at = NULL`.
5. Reminders (`jobs/reminders.go`, every 5 minutes): meetings starting within 24 hours with `reminder_sent_at IS NULL` and not canceled. Recipients follow the group's `reminder_audience`: `rsvped` sends to RSVP'd emails only; `all_members` sends to RSVP'd emails plus members with `meeting_reminder = true`. Opt-outs are always excluded. Set `reminder_sent_at` after enqueueing.
6. Virtual links are included only for recipients who RSVP'd.
7. Sender: every email comes from one address on the shared sending domain (e.g. `notifications@…`), with the group's name as the display name for group emails. Per-group sending domains are deferred.
8. Custom messages: compose form; members with `custom_message = true`; limit 3 per group per day.
9. Preferences page: four HTMX toggles per group.

### Phase 7: Attendance & Analytics (2–3 days)

1. Attendance page for past, non-canceled meetings: RSVP list with checkboxes (name, or email if no name). Saving sets all to false, then true for the checked IDs, in one transaction.
2. Analytics queries (all scoped to `$1 = group_id`, rendered as tables):

```sql
-- RSVPs and attendance per meeting (last 20)
SELECT m.title, m.starts_at,
       COUNT(r.id)                          AS rsvps,
       COUNT(r.id) FILTER (WHERE r.attended) AS attended
FROM meetings m
LEFT JOIN rsvps r ON r.meeting_id = m.id
WHERE m.group_id = $1 AND NOT m.canceled
GROUP BY m.id
ORDER BY m.starts_at DESC
LIMIT 20;

-- Member growth: new and running total per month (last 12 months shown)
SELECT month, new_members, SUM(new_members) OVER (ORDER BY month) AS total_members
FROM (
    SELECT date_trunc('month', joined_at) AS month, COUNT(*) AS new_members
    FROM memberships WHERE group_id = $1
    GROUP BY 1
) m
ORDER BY month DESC
LIMIT 12;

-- Most active members (top 20)
WITH r AS (
    SELECT r.user_id, COUNT(*) AS rsvps, COUNT(*) FILTER (WHERE r.attended) AS attended
    FROM rsvps r JOIN meetings m ON m.id = r.meeting_id
    WHERE m.group_id = $1 AND r.user_id IS NOT NULL
    GROUP BY r.user_id
), p AS (
    SELECT user_id, COUNT(*) AS posts FROM posts WHERE group_id = $1 GROUP BY user_id
)
SELECT u.name, u.email,
       COALESCE(r.rsvps, 0) AS rsvps, COALESCE(p.posts, 0) AS posts, COALESCE(r.attended, 0) AS attended
FROM memberships mem
JOIN users u ON u.id = mem.user_id
LEFT JOIN r ON r.user_id = u.id
LEFT JOIN p ON p.user_id = u.id
WHERE mem.group_id = $1
ORDER BY COALESCE(r.rsvps, 0) + COALESCE(p.posts, 0) + COALESCE(r.attended, 0) DESC
LIMIT 20;

-- Meetings per month (last 12)
SELECT date_trunc('month', starts_at) AS month, COUNT(*) AS meetings
FROM meetings WHERE group_id = $1 AND NOT canceled
GROUP BY 1 ORDER BY 1 DESC LIMIT 12;
```

Member growth counts current members only (people who left aren't included). "No data yet" states for empty tables; attendance rate shows "N/A" when a meeting had no RSVPs.

### Phase 8: Directory & Discovery (2–3 days)

1. `location.js` on the landing page and directory:
   - If a saved location exists (localStorage), use it.
   - Otherwise ask the browser for geolocation. On success, round to 1 decimal place (about 11 km) and save.
   - If geolocation is denied or unavailable, show a "Where are you?" city prompt (Place Autocomplete, cities only) and save the rounded result.
   - Navigate to `/directory?lat=…&lng=…`. The server never reads location from cookies, so pages stay cacheable.
2. Distance query (NULL coordinates sort last):

```sql
SELECT g.*,
       6371 * 2 * asin(sqrt(
           power(sin(radians(g.latitude - $1) / 2), 2) +
           cos(radians($1)) * cos(radians(g.latitude)) *
           power(sin(radians(g.longitude - $2) / 2), 2)
       )) AS distance_km
FROM groups g
WHERE g.suspended_at IS NULL
  AND ($3 = '' OR g.search_vector @@ websearch_to_tsquery('english', $3))
ORDER BY distance_km ASC NULLS LAST, g.id DESC
LIMIT 20 OFFSET $4;
```

3. Cards show name, topic, city, distance (miles in the US), member count. Empty and no-results states.
4. Topic filter, pagination, SEO tags (title, description, Open Graph) on group and meeting pages.

### Phase 9: CDN (3–4 days)

1. **Spike first (half a day):** set up a test pull zone with the origin at `origin.yourcommunity.com` and one test custom hostname. Determine how the app learns the visitor's hostname: with Forward Host Header on, does Bunny connect using the origin's name for TLS while sending the visitor's hostname as `Host`, and what does Caddy need to accept it? Finalize the Caddyfile and `site/site.go`'s hostname source from the result.
2. DNS: `yourcommunity.com` → CNAME to the pull zone (or Bunny DNS if you want the apex); `origin.yourcommunity.com` → droplet.
3. Cache headers per route category; `/sw.js` short-lived; `/static/*` immutable.
4. Implement `cdn/bunny.go` and `PurgePaths`; wire every mutation.
5. Verify that no cacheable response ever contains emails or virtual links (add a test that renders public pages and greps for `@` and `virtual_url` values).
6. Log purge failures.

### Phase 10: Paid Tier & Custom Domains (4–5 days)

1. **Stripe:**
   - One product and a $20/year price. `POST /upgrade` creates a Checkout session with `group_id` and the payer's user ID in metadata.
   - Webhooks (`checkout.session.completed`, `customer.subscription.updated`, `customer.subscription.deleted`, `invoice.payment_failed`) are processed idempotently by event ID.
   - The success page shows "Processing…" until the webhook has updated the tier.
2. **Going paid:** `tier='paid'`, `member_limit=NULL`, `storage_limit_bytes=25 GB`.
3. **Lapse (subscription deleted or past period end):**
   - Set `tier='free'`, `member_limit=150`, `storage_limit_bytes=2 GB`, `branding='{}'`.
   - Remove the custom domain: Bunny `RemoveHostname`, remove it from the Google browser key's allowed websites, clear the columns, refresh the site cache.
   - Purge the group's pages.
4. **Customer portal** link for card updates and cancellation.
5. **Branding:** validated hex color, logo upload (image, max 2 MB; cover images and logos don't count toward storage). Logo and group name replace platform identity on the custom domain.
6. **Custom domain lifecycle:**
   - `POST /domain`: validate the format (not an IP, must have a dot, not our own domains, not already claimed), call `AddHostname`, set `pending_dns`, and show the CNAME instructions (warn if it's an apex domain).
   - `POST /domain/verify`: resolve the CNAME to confirm it points at the pull zone.
   - Once it does: call `LoadFreeCertificate`, add the domain to the Google browser key's allowed websites (manually at first; automatable with Google's API Keys API), set `active`, refresh the site cache.
   - `DELETE /domain` reverses all of it.
7. **White-label checks on a custom domain:**
   - Base layout, email content, manifest, error pages, and legal pages show no platform name or links (the sender address on the shared domain is the accepted v1 exception).
   - Platform-only routes 404.
   - Login and admin work end to end on the custom domain (the host-bound sessions from Phase 3).
8. **Delete group:** cancel the Stripe subscription, remove the custom domain, delete every Spaces object under `groups/{group_id}/`, delete the group row (cascades), purge.

### Phase 11: PWA (1–2 days)

1. `GET /manifest.json` generated per host: platform name and icons on the main domain; group name, logo, and brand color on custom domains.
2. Service worker:
   - Cache-first for static assets.
   - Network-first for pages, with fallback to the cached page or `/offline`.
   - Network-only for fragments and every POST/DELETE (no offline action queue).
3. Install prompt (Chromium `beforeinstallprompt`; a short "Add to Home Screen" hint on iOS).
4. Responsive audit at 320 / 768 / 1024+ px; 44 px minimum touch targets.

### Phase 12: Platform Admin, Compliance & Deploy (3–4 days)

1. **Platform admin:** group list, suspend/restore (a suspended group's pages show "This group is unavailable"; it is hidden from the directory; its pages are purged). Platform admins can delete any post.
2. **Legal:** terms and privacy pages; unbranded on custom domains.
3. **Cleanup job** (hourly): delete expired sessions and magic links older than a day.
4. **Email DNS:** SPF, DKIM (from Resend), and DMARC records for the sending domain before sending real mail.
5. **Hardening:**
   - Error pages (404, 500).
   - Rate limits: magic links 3 per email per 15 minutes; RSVPs 10 per IP per minute; custom messages 3 per group per day.
   - Input validation; structured `slog` logging.
6. **Provision:** 2 GB droplet; firewall allowing only SSH, 80, and 443 (optionally restrict 80/443 to Bunny's published edge IPs); Spaces bucket (no CORS needed: uploads go through the app).
7. **Deploy:** DNS per Phase 9; Bunny pull zone → `origin.yourcommunity.com`; `make deploy`.
8. **Backups:** nightly `pg_dump` uploaded to a private Spaces bucket, 14-day retention; test a restore once.
9. **Monitoring:** DigitalOcean alerts (CPU, memory, disk); log-based check that the reminder and cleanup jobs run.
10. **Smoke test on both the main domain and a custom domain:**
    - Directory with location.
    - Anonymous RSVP, the "you're going" state on a return visit, and cancel via the email link.
    - Magic link login (including the confirm page).
    - Join and leave.
    - Post, reply, and upload; storage limit.
    - Co-organizer invite and acceptance; ownership transfer.
    - Notification toggles and one-click unsubscribe.
    - Reminder email (time zone correct).
    - Attendance and analytics.
    - Upgrade, domain activation, and lapse.
    - PWA install.
