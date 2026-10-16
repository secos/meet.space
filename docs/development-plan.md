# Community Groups Platform — Development Plan

This document turns the PRD into concrete implementation details: hosting topology, project structure, packages, database schema, routes, security rules, template patterns, deployment configuration, and a phase-by-phase build plan.

**Companion documents:** `prd.md` (requirements), `workflows.md` (screen flows and sequence diagrams), `design.md` (visual design), `decisions.md` (decisions made and questions still open). `design.md` is the source of truth for colors, typography, spacing, components, and layouts; Tailwind classes shown in this document are illustrative.

---

## Hosting & Domain Topology

```
Visitor ──HTTPS──▶ BunnyCDN pull zone ──HTTPS──▶ Caddy (origin.yourcommunity.com) ──▶ Go app ──▶ Postgres
   yourcommunity.com         (cache, custom-domain SSL)     EC2 t4g.small + Elastic IP     │  (same instance)
   community.example.com                                                                  ├──▶ S3 (private bucket: uploads, backups)
                                                                                          ├──▶ SES (auth. and mail. streams)
                                                                          SNS ──HTTPS──▶  └──◀ /webhooks/ses (bounces, complaints)
```

- `yourcommunity.com` (public): CNAME to the Bunny pull zone. Never points at the EC2 instance.
- `origin.yourcommunity.com` (origin): A record to the instance's Elastic IP. Caddy gets its Let's Encrypt certificate for this name.
- Custom domains: CNAME to the pull zone, registered as hostnames on it.
- The pull zone's cache key includes the hostname ("Requested Hostname" option). Without it, `/` on a custom domain and `/` on the main domain would share one cache entry.
- AWS access uses the instance's IAM role. There are no static AWS keys in `.env`.
- The Go app must know the visitor's hostname for every request. How Bunny passes it to the origin is verified in a Phase 9 spike before custom domains are built (see Phase 9). Until then `site/site.go` reads the hostname from a configurable source (`HOST_SOURCE`: the `Host` header or a named header), so the spike result is a config change.

### Site Context (Host Resolution)

The first middleware on every request resolves the visitor's hostname into a **site context**:

| Hostname | Context | Behavior |
|----------|---------|----------|
| `PUBLIC_HOST` | Platform | Full platform: directory, all groups, create group, cross-group dashboard, platform admin |
| An **active** custom domain | Group (that group only) | `/` renders the group page. Only that group's pages, login, account settings, and admin are served. Directory, other groups, group creation, and platform admin return 404. No platform branding or links. |
| `ORIGIN_HOST` (`origin.yourcommunity.com`) | Origin only | Serves only `/health` and `/webhooks/*` (called by SNS and Stripe directly, not through the CDN). Everything else 404s. |
| Anything else | None | 404 |

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
│   │   ├── security.go              # Origin check on state-changing requests, client IP from CDN header, Referrer-Policy on token pages
│   │   ├── cache.go                 # Cache-Control helpers (public vs no-store route classes)
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
│   │   ├── files.go                 # Streams uploads from S3 on the visitor's hostname (immutable, cacheable)
│   │   ├── pwa.go                   # Per-host manifest.json, service worker, offline page
│   │   └── webhook.go               # Stripe webhooks, SES/SNS event webhook
│   ├── model/                       # Types + queries: user.go, group.go, meeting.go, post.go, notification.go, subscription.go
│   ├── email/
│   │   ├── email.go                 # Sender interface (Send(ctx, Message)); dev implementation logs to stdout
│   │   ├── ses.go                   # SES v2 client: per-stream From, custom headers, configuration sets
│   │   ├── outbox.go                # Enqueue in the caller's transaction; poller claims due rows, sends, retries with backoff
│   │   ├── events.go                # SNS message verification, bounce/complaint handling, suppression, group pause
│   │   └── guard.go                 # Pre-send checks: suppression list, opt-outs, per-address limits, paused groups
│   ├── storage/s3.go                # Upload/delete/stream in S3 (local ./uploads directory in dev)
│   ├── cdn/bunny.go                 # Purge, custom hostnames, free certificates
│   ├── token/token.go               # HMAC-signed tokens (RSVP cancel, unsubscribe, RSVP cookie)
│   ├── branding/                    # looks.go (allowlist of looks), palette.go (hex → on-brand text, derived accents, light and dark tints)
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
│                                    # meeting_changed, custom_message, coorganizer_invite, ownership_transferred
├── static/
│   ├── css/output.css               # Built by Tailwind CLI (not committed)
│   ├── js/htmx.min.js               # Vendored
│   ├── js/places.js                 # Place Autocomplete wiring for admin forms and the directory city prompt
│   ├── js/location.js               # Directory: browser geolocation → rounded coords → navigate
│   ├── js/sw.js                     # Service worker source
│   └── img/                         # Default icons
├── input.css                        # Tailwind v4 entry: @import "tailwindcss" + @theme tokens from design.md
├── Dockerfile, docker-compose.yml, docker-compose.prod.yml, Caddyfile, Makefile
├── docs/                            # prd.md, development-plan.md, workflows.md, design.md, decisions.md, research notes
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
| Object storage | `github.com/aws/aws-sdk-go-v2` (`config` + `service/s3`) | Credentials come from the EC2 instance role |
| Email | `github.com/aws/aws-sdk-go-v2/service/sesv2` | Handles SigV4. Use `SendEmail` with the `Simple` content type and custom `Headers`. **Do not use `ListManagementOptions`**: SES would override our unsubscribe headers and host its own page. |
| Email events | SNS HTTPS subscription → `/webhooks/ses` | Verify the SNS message signature (stdlib `crypto/x509`, `crypto/rsa`); handle the subscription-confirmation message |
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
    -- A single leading "/", then no "/" or "\" straight after it, and no "\" or control
    -- character anywhere: browsers read "\" as "/" and drop tabs and line breaks, so
    -- "/\evil.com" would leave the site (decisions.md "Return paths").
    redirect_path TEXT CHECK (redirect_path IS NULL
                              OR redirect_path ~ '^/([^/\\[:cntrl:]][^\\[:cntrl:]]*)?$'),
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
    cover_image_key      TEXT,                           -- S3 key
    members_can_post     BOOLEAN NOT NULL DEFAULT TRUE,
    reminder_audience    TEXT NOT NULL DEFAULT 'rsvped'
                         CHECK (reminder_audience IN ('rsvped', 'all_members')),
    tier                 TEXT NOT NULL DEFAULT 'free' CHECK (tier IN ('free', 'paid')),
    member_limit         INT DEFAULT 150,                -- NULL = unlimited (paid)
    storage_limit_bytes  BIGINT NOT NULL DEFAULT 2147483648,  -- 2 GB free; 25 GB paid
    custom_domain        TEXT UNIQUE CHECK (custom_domain = lower(custom_domain)),
    custom_domain_status TEXT CHECK (custom_domain_status IN ('pending_dns', 'active')),
    look                 TEXT NOT NULL DEFAULT 'clean' CHECK (look IN ('clean', 'lively', 'confetti', 'poster')),  -- Free for every group
    branding             JSONB NOT NULL DEFAULT '{}',    -- Paid only: {"primary_color": "#4f46e5", "logo_key": "..."}; empty means the platform's default color and no logo
    suspended_at         TIMESTAMPTZ,                    -- Set by platform admin
    email_paused_at      TIMESTAMPTZ,                    -- Set automatically when complaint/bounce rates are too high; blocks notification emails
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
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (id, group_id)                                -- Lets a meeting comment require its meeting's group
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
-- The two composite keys make the database refuse a reply to anything but a feed post in the
-- same group, and a comment on another group's meeting (decisions.md "Reply rules in the
-- database"). A NULL parent_id or meeting_id skips its key.
CREATE TABLE posts (
    id           UUID PRIMARY KEY,
    group_id     UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    meeting_id   UUID,
    parent_id    UUID,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content      TEXT NOT NULL CHECK (length(content) > 0),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    is_feed_post BOOLEAN GENERATED ALWAYS AS (meeting_id IS NULL AND parent_id IS NULL) STORED,
    is_reply     BOOLEAN GENERATED ALWAYS AS (parent_id IS NOT NULL) STORED,
    CHECK (meeting_id IS NULL OR parent_id IS NULL),
    UNIQUE (id, group_id, is_feed_post),
    CONSTRAINT posts_meeting_fkey FOREIGN KEY (meeting_id, group_id)
        REFERENCES meetings (id, group_id) ON DELETE CASCADE,
    -- A reply (is_reply = true) must point at a post in its own group with is_feed_post = true.
    CONSTRAINT posts_parent_fkey FOREIGN KEY (parent_id, group_id, is_reply)
        REFERENCES posts (id, group_id, is_feed_post) ON DELETE CASCADE
);
CREATE INDEX idx_posts_feed ON posts (group_id, id DESC) WHERE meeting_id IS NULL AND parent_id IS NULL;
CREATE INDEX idx_posts_replies ON posts (parent_id, id) WHERE parent_id IS NOT NULL;
CREATE INDEX idx_posts_meeting ON posts (meeting_id, id) WHERE meeting_id IS NOT NULL;
CREATE INDEX idx_posts_user ON posts (user_id);

-- Database cascades delete attachment rows but not the files in S3.
-- The app deletes S3 objects first, then the rows (see Phase 4).
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

-- A group can briefly have two rows during an ownership transfer: the old owner's
-- (cancel_at_period_end = TRUE, ending) and the new owner's (starts as 'trialing' until the old period ends,
-- so nobody pays twice). The unique index allows only one renewing subscription per group.
CREATE TABLE subscriptions (
    id                     UUID PRIMARY KEY,
    group_id               UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    payer_user_id          UUID REFERENCES users(id) ON DELETE SET NULL,
    stripe_customer_id     TEXT NOT NULL,
    stripe_subscription_id TEXT NOT NULL UNIQUE,
    status                 TEXT NOT NULL CHECK (status IN
                           ('trialing', 'active', 'past_due', 'unpaid', 'incomplete', 'canceled')),
    current_period_end     TIMESTAMPTZ NOT NULL,
    cancel_at_period_end   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_one_renewing_subscription ON subscriptions (group_id)
    WHERE status IN ('trialing', 'active', 'past_due') AND NOT cancel_at_period_end;
CREATE INDEX idx_subscriptions_group ON subscriptions (group_id);
-- A group has paid features while any of its subscriptions is 'trialing', 'active' or 'past_due'.

CREATE TABLE stripe_events (
    event_id     TEXT PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Every email goes through the outbox. SES v2 has no idempotency key, so dedupe_key prevents
-- the app from enqueueing the same logical email twice (e.g. 'rsvp_confirm:{meeting_id}:{email}').
CREATE TABLE email_outbox (
    id              UUID PRIMARY KEY,
    stream          TEXT NOT NULL CHECK (stream IN ('auth', 'notify')),   -- auth. vs mail. subdomain and configuration set
    kind            TEXT NOT NULL,                                        -- magic_link, rsvp_confirmation, meeting_reminder, ...
    group_id        UUID REFERENCES groups(id) ON DELETE SET NULL,        -- for per-group complaint tracking
    to_email        TEXT NOT NULL CHECK (to_email = lower(to_email)),
    from_name       TEXT NOT NULL,
    subject         TEXT NOT NULL,
    body_html       TEXT NOT NULL,
    body_text       TEXT NOT NULL,
    headers         JSONB NOT NULL DEFAULT '{}',                          -- List-Unsubscribe, List-Unsubscribe-Post, Reply-To
    dedupe_key      TEXT NOT NULL UNIQUE,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'sending', 'sent', 'failed', 'dead', 'suppressed')),
    attempts        INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ses_message_id  TEXT,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at         TIMESTAMPTZ
);
-- 'sending' rows are included so a row whose sender crashed mid-send is found again once its
-- 2-minute claim (next_attempt_at, set when the row is taken) runs out (decisions.md).
CREATE INDEX idx_outbox_due ON email_outbox (stream, next_attempt_at) WHERE status IN ('pending', 'failed', 'sending');
CREATE INDEX idx_outbox_message ON email_outbox (ses_message_id) WHERE ses_message_id IS NOT NULL;

-- Addresses we must not send to again. Applies platform-wide.
CREATE TABLE email_suppressions (
    email      TEXT PRIMARY KEY CHECK (email = lower(email)),
    reason     TEXT NOT NULL CHECK (reason IN ('hard_bounce', 'complaint', 'manual')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Delivery events from SES (via SNS), tied to a group through the outbox row.
CREATE TABLE email_events (
    id         UUID PRIMARY KEY,
    outbox_id  UUID REFERENCES email_outbox(id) ON DELETE SET NULL,
    group_id   UUID REFERENCES groups(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN ('delivery', 'bounce_hard', 'bounce_soft', 'complaint', 'reject')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_email_events_group ON email_events (group_id, created_at);
```

```sql
-- 001_initial_schema.down.sql
DROP TABLE IF EXISTS email_events, email_suppressions, email_outbox, stripe_events,
    subscriptions, email_opt_outs, notification_preferences, attachments,
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
4. **Private pages:** `Cache-Control: no-store, private`. Everything not listed as a cacheable page or a file: `/auth/*`, `/account`, `/settings/*`, `/unsubscribe/*`, `/rsvp/cancel/*`, `/admin/*`, `/platform/*`, `/offline`-style error pages. Pages with a token in the URL (`/auth/verify/*`, `/unsubscribe/*`, `/rsvp/cancel/*`) also send `Referrer-Policy: no-referrer`. A middleware applies this by default, and a route opts in to caching explicitly.
5. **Files:** `Cache-Control: public, max-age=31536000, immutable`. Keys are UUIDv7, so a file never changes.

### Cacheable Pages

```
GET /                                   → Platform: landing page. Custom domain: the group page.
GET /directory                          → Directory (platform only). Query: q, topic, lat, lng (rounded), page
GET /groups/{slug}                      → Group page (custom domain: only its own slug)
GET /groups/{slug}/meetings/{id}        → Meeting page (no virtual link, no emails)
GET /terms, GET /privacy                → Legal pages (unbranded on custom domains)
GET /files/{group_id}/{name}            → Upload, streamed from the private S3 bucket on the visitor's own hostname (immutable)
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
POST   /admin/groups/{slug}/look               → Choose a look (OWNER, any tier)
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
POST /webhooks/ses                      → SNS messages: subscription confirmation, bounces, complaints, deliveries (SNS signature verified; origin hostname only)
GET  /manifest.json                     → Per-host manifest (group name and icons on custom domains)
GET  /sw.js                             → Service worker (root scope, short cache)
GET  /offline                           → Offline fallback page
GET  /static/*                          → Fingerprinted assets, cached for a year
GET  /health                            → DB ping (reachable on the origin hostname)
```

---

## Security Rules

These apply everywhere and are checked in code review.

1. **Return paths:** only relative paths starting with a single `/` are accepted, with no backslash or control character anywhere (browsers turn `/\evil.com` and `/<tab>/evil.com` into `//evil.com`). Also enforced by a CHECK constraint. Anything else falls back to `/admin`.
2. **Hostnames in emails:** links in emails are only built for `PUBLIC_HOST` or an **active** custom domain from the site cache, never from an unvalidated `Host` header.
3. **Magic links:** GET shows a confirmation page; only POST consumes the token. Tokens are single-use and expire after 15 minutes. The verifying request's hostname must match `magic_links.host`.
4. **Sessions:** host-only cookies (no `Domain` attribute), `HttpOnly`, `Secure`, `SameSite=Lax`, 30-day expiry. A session is only honored on the hostname stored in `sessions.host`.
5. **CSRF:** `SameSite=Lax` plus an Origin/Referer check on every POST/DELETE (the origin must equal the current hostname).
6. **Signed tokens:** RSVP-cancel links, unsubscribe links, and the anonymous RSVP cookie are HMAC-SHA256 signed with `APP_SECRET` and include an expiry where appropriate.
7. **Private data never in cacheable HTML:** no emails, no virtual meeting links. They only appear in `no-store` fragments and in emails.
8. **Client IP:** taken from the CDN's forwarded-for header only when the request comes from the origin proxy; used for rate limiting.
9. **User content:** rendered through `html/template` escaping; never rendered as raw HTML. Uploads are validated by sniffed content type, not just extension.
10. **Branding values:** colors must match `^#[0-9a-fA-F]{6}$` before being written into a `<style>` block. The look must be one of the allowlisted names (`clean`, `lively`, `confetti`, `poster`), enforced by a CHECK constraint and by the handler. Every color written into the page (brand, text-on-brand, derived accents and tints) is computed by the server from the validated hex, never taken from the request.
11. **Webhooks:** `/webhooks/stripe` and `/webhooks/ses` verify the sender's signature before doing anything. They are exempt from the Origin check, and they are served only on the origin hostname, never on a custom domain.
12. **Email content:** emails to unverified addresses (RSVP confirmations) never repeat free text typed by the visitor (name, notes). Email bodies come from templates with data the platform controls.
13. **Private bucket:** the S3 bucket blocks all public access. Files reach visitors only through `/files/...`, and the instance role is limited to that bucket and to SES sending from the platform's verified identities.

---

## Template Patterns: Cached Shell + Dynamic Fragments

> Styling here is illustrative. Use `design.md` for real tokens and components.

### Tailwind v4 Entry (`input.css`)

```css
@import "tailwindcss";

/* Default theme tokens, taken from design.md. The accent and tint values are placeholders; palette.go computes them. */
@theme {
  /* Neutrals (light values; dark values are set under prefers-color-scheme). Full list in design.md §2. */
  --color-ground: #f5f7fa;
  --color-surface: #ffffff;
  --color-ink: #172033;
  --color-muted: #5d6779;
  --color-line: #e3e8ef;
  /* Group-overridable (computed by internal/branding/palette.go) */
  --color-brand: #2563eb;
  --color-brand-contrast: #ffffff;
  --color-brand-text: #1d4ed8;
  --color-accent-1: #5fc9bd;  --color-accent-2: #f0c35a;  --color-accent-3: #e788b2;
  --color-tint-1: #e8effd;  --color-tint-2: #dff5f1;  --color-tint-3: #fdf0d2;  --color-tint-4: #fbe0ec;
  --font-sans: "Figtree", ui-sans-serif, system-ui, sans-serif;
  --font-confetti: "Fredoka", "Figtree", ui-sans-serif, system-ui, sans-serif;
  --font-poster: "Unbounded", "Figtree", ui-sans-serif, system-ui, sans-serif;
}
```

Because `@theme` tokens are real CSS custom properties, paid-group branding is a small override block in the page head (see below). Utilities like `bg-brand` and `text-brand-contrast` pick it up automatically. The overridable tokens are `--color-brand`, `--color-brand-contrast`, three derived accents (`--color-accent-1..3`) and four tints (`--color-tint-1..4`), with separate dark values. `design.md` is the reference for how each look uses them.

### Base Layout (`templates/layouts/base.html`)

```html
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{block "title" .}}{{.SiteName}}{{end}}</title>
    <link rel="stylesheet" href="/static/css/output.css?v={{.AssetVersion}}">
    {{if .Palette}}
    {{/* Server-computed from the validated hex (branding/palette.go); explicit hex values only. */}}
    <style>:root { --color-brand: {{.Palette.Brand}}; --color-brand-contrast: {{.Palette.OnBrand}};
      --color-accent-1: {{.Palette.Accent1}}; --color-accent-2: {{.Palette.Accent2}}; --color-accent-3: {{.Palette.Accent3}};
      --color-tint-1: {{.Palette.Tint1}}; /* … tint-2..4, plus dark values under the dark-mode selector */ }</style>
    {{end}}
    <link rel="manifest" href="/manifest.json">
    <meta name="theme-color" content="{{.ThemeColor}}">
    <link rel="apple-touch-icon" href="{{.IconURL}}">
    <script src="/static/js/htmx.min.js?v={{.AssetVersion}}"></script>
    <script>if ('serviceWorker' in navigator) navigator.serviceWorker.register('/sw.js');</script>
    {{block "head" .}}{{end}}
</head>
<body class="bg-gray-50 text-gray-900 min-h-screen flex flex-col" data-look="{{.Look}}">
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

`SiteName`, `FooterText`, `IconURL`, `ThemeColor`, `Look`, `Palette` and `Branding` come from the site context and the group being shown, so the same templates serve both the platform and white-label domains. `Look` and `Palette` describe the group being shown, so a group's pages use its look on the platform host and on its custom domain alike. A group without its own color gets a palette derived from the platform's default blue. Platform pages (landing, directory, platform login) and all admin pages always use Clean with the default palette.

### Looks (Clean, Lively, Confetti, Poster)

A group's look is `groups.look` (default `clean`, free for every group). The four looks share one set of pages, fragments and tokens. They differ in two ways:

1. **CSS.** Each look's rules are scoped under `[data-look="…"]` on `<body>` and compiled into the one stylesheet (Tailwind scans all of them). Clean has no scope: it is the base. Dark values follow the same dark-mode mechanism as the base tokens.
2. **A few template partials.** Where a look changes structure, not just style, the page includes a look-specific partial by name, with `clean` as the fallback if a look has no override:
   - `meeting_header` (Poster's flood hero and date numeral, Confetti's sticker and tiles, Lively's cover and date tile),
   - `rsvp_going` (the "You're going" state: plain, avatars, "You're in!", ticket stub),
   - `group_header`, and the "coming up" row.
   Everything else (forms, feed, comments, admin) is shared and only restyled by CSS. The admin area always uses Clean, whatever the group's look.

Rules that keep this safe:
- **Cache:** the look is part of the group's data, so every page is already per group. Changing it purges the group's pages (the "look or branding changed" row in the table below), and fragments are `no-store`.
- **Private data:** the look only changes presentation. The cached shell still never contains emails or virtual links, in any look.
- **Motion:** every animation (drifting header glow, count tick, avatar pop, sticker drop, confetti, scrolling strip, ticket drop) is disabled under `prefers-reduced-motion`. The state change itself (form → going) never depends on an animation.
- **Scripts:** the Confetti burst is a small script (`static/js/confetti.js`, about 1 KB) loaded only on pages of groups using that look. It reads colors from the page's tokens and does nothing under reduced motion.
- **Fonts:** Figtree for all looks, plus Fredoka (Confetti) and Unbounded (Poster). Font files are self-hosted and loaded only for the look that needs them.
- **Long content:** every look must handle a 60-character meeting title, a group name with no spaces, and a 5-line description without clipping. Poster reduces the title size above about 40 characters.
- **Email:** emails use one layout and the brand color only, in every look.
- **PWA:** `manifest.json` and `theme-color` use the brand color. The look does not change them.

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
| Group info, branding or look changed, suspended, restored | `/groups/{slug}`, `/` (plus directory pages on the main host) |

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
      ORIGIN_HOST: origin.localhost:8080      # Serves /health and /webhooks/* only
      HOST_SOURCE: host                       # "host" or the name of a header; set from the Phase 9 spike
      SES_REGION: ""                          # Empty → emails are logged to stdout and written to the outbox only
      SES_CONFIG_SET_AUTH: ""
      SES_CONFIG_SET_NOTIFY: ""
      SES_FROM_AUTH: ""                       # e.g. login@auth.yourcommunity.com
      SES_FROM_NOTIFY: ""                     # e.g. notifications@mail.yourcommunity.com
      SES_REPLY_TO: ""
      SNS_TOPIC_ARN: ""                       # Only this topic is accepted at /webhooks/ses
      BUNNY_API_KEY: ""                       # Empty → CDN calls are no-ops
      BUNNY_PULL_ZONE_ID: ""
      S3_REGION: ""                           # Empty → uploads go to a local ./uploads dir
      S3_BUCKET: ""
      # No AWS keys here. On EC2 the SDK uses the instance role.
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
    env_file: .env                          # Same variables as dev, with production values (no AWS keys; the instance role supplies them)
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

This works as-is if Bunny connects with the origin hostname and passes the visitor's hostname to the app some other way. If instead Bunny forwards the visitor's hostname as the `Host` header, Caddy also needs a block that accepts any hostname on the origin's certificate. The Phase 9 spike decides which. SNS and Stripe call `origin.yourcommunity.com` directly, so the block above must stay reachable without the CDN.

**EC2 notes.** Attach an Elastic IP so the origin DNS record survives stop/start. The security group allows 22 (your IP only), 80 and 443. The instance role needs: `s3:GetObject/PutObject/DeleteObject/ListBucket` on the uploads and backups buckets, and `ses:SendEmail` limited to the platform's verified identities. Put Postgres data on its own EBS volume so the instance can be replaced without losing it, and take daily EBS snapshots.

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

**Estimated total: 10–12 weeks solo** (includes Phase 4b, the three extra looks). Phases 0–4 give you a working product for your own groups; the rest can overlap.

### Before Phase 0: accounts and lead-time items (start now, they run in parallel)

1. **SES production access.** In one region, verify the sending domain, then submit the production-access request (choose Transactional or Marketing honestly, give the website URL, acknowledge opt-in-only sending, and ask for the daily quota you need). First response takes up to 24 hours and AWS can decline without detail. In the same account run `PutAccountPricingAttributes Plan=NONE` so billing is $0.10 per 1,000, not the $0.16 default. Use a paid account, not the free plan, which closes the account after 6 months.
2. **Domains and DNS:** register the platform domain; plan `auth.` and `mail.` subdomains with their own DKIM, custom MAIL FROM (for SPF alignment) and a DMARC record at `p=none` with reports.
3. **Other accounts:** Bunny (pull zone), Stripe (needs a legal entity, see `decisions.md`), Google Cloud project with billing and two restricted Maps keys, and an AWS account with an IAM role for the instance.
4. **`design.md`** is written and agreed (Phase 0 and Phase 1 depend on it).

### Phase 0: Scaffolding (3–4 days)

1. `go mod init`; create the directory structure above.
2. `cmd/server/main.go`: load config, open the DB pool, register routes, start background workers, and handle the `migrate up|down` subcommand. Migrations never run automatically on startup.
3. `internal/config`: `DATABASE_URL`, `PUBLIC_HOST`, `ORIGIN_HOST`, `HOST_SOURCE`, `PUBLIC_BASE_URL`, `APP_SECRET`, SES (region, two configuration sets, two From addresses, reply-to, SNS topic ARN), Bunny, S3, Stripe, both Google keys, `ENV`.
4. Docker Compose; migration 001.
5. Tailwind v4 standalone CLI; `input.css` with `@theme` tokens taken from `design.md`.
6. Vendor `htmx.min.js` and self-host the font files (WOFF2, fingerprinted, served from the visitor's own hostname, so white-label domains never contact a font provider); `tmpl/render.go` with layout composition and site-context template data (`SiteName`, `FooterText`, `Branding`, `Look`, etc.).
7. `site/site.go`: host → site context middleware, custom-domain cache, `BaseURL(group)`.
8. `middleware/security.go`: Origin check on POST/DELETE; client IP extraction. `middleware/cache.go`: `no-store, private` by default, with an explicit opt-in for cacheable pages, so a forgotten route fails safe.
9. `token/token.go`: HMAC sign/verify with expiry.
10. `email`: the `Sender` interface, the `email_outbox` poller and a dev implementation that logs to stdout, so every later phase enqueues through the outbox from the start. The SES client itself lands in Phase 6.
11. Base layout, a placeholder home page, and `/health`.
12. Verify: `make dev` → styled page at `localhost:8080`, and `hikers.localhost:8080` resolves to a group context.

### Phase 1: Groups & Meetings (3–4 days)

Group creation needs a logged-in user. Build a minimal magic-link login first (request → email logged to stdout → verify → session) and finish it in Phase 3.

1. `model/group.go`: CRUD. Creating a group inserts an `owner` membership and default notification preferences in the same transaction. The slug is generated from the name (lowercase, hyphenated, max 60 chars, `-2`/`-3` suffix on collision) and never changes afterward.
2. Group form: name, description, topic, location via Place Autocomplete restricted to cities (fills `location`, `latitude`, `longitude`), timezone (select, defaulted from the chosen city; editable), cover image, `members_can_post`, `reminder_audience`.
3. `model/meeting.go`: CRUD. `timezone` defaults to the group's. The form's local date and time are interpreted in the meeting's timezone and stored as UTC.
4. Meeting form: in-person → Place Autocomplete (fills `location`, `place_id`, `latitude`, `longitude`) + map preview; virtual → `virtual_url`.
5. `RequireUser`, `RequireOrganizer(slug)`, `RequireOwner(slug)` middleware; permissions follow the route table.
6. Public group and meeting pages. Times render in the meeting's timezone with the zone abbreviation. `virtual_url` is never passed to public page templates.
7. Dashboard: on the platform, groups the user owns, co-organizes, and belongs to; on a custom domain, only that group.
8. Styling per `design.md`. Build only the Clean look now, but structure it as the base with look-specific partials as named includes (`meeting_header`, `rsvp_going`, `group_header`), so the other looks in Phase 4b add files instead of rewriting templates.

### Phase 2: RSVP (2–3 days)

1. `CreateRSVP`: lowercase the email; `INSERT … ON CONFLICT (meeting_id, email) DO NOTHING` (a duplicate is treated as success).
2. Logged in: the email comes from the session, never from the form. Anonymous: email (required) + name (optional).
3. Anonymous RSVPs set a signed, host-only cookie listing the visitor's RSVP IDs, so the RSVP fragment can show "You're going" and the virtual link on return visits.
4. RSVP fragment states: form / going (with virtual link and "Can't make it") / closed (past or canceled).
5. Cancel: `DELETE` for logged-in users or cookie holders; signed `/rsvp/cancel/{token}` links for email (GET confirm page, POST cancel).
6. RSVP confirmation email, enqueued into `email_outbox` in the same transaction as the RSVP (dedupe key `rsvp_confirm:{meeting_id}:{email}`): time in the meeting's timezone, virtual link (if any), "Can't make it" link, all links built with `BaseURL(group)`. It does not repeat the visitor's typed name.
7. Organizer RSVP list (name, or email if no name).
8. Abuse limits on the anonymous form, since it can trigger mail to any address: 10 RSVPs per client IP per minute, 5 confirmation emails per address per hour across all meetings, a syntax and MX check (never an SMTP probe), no email to suppressed addresses (the RSVP still succeeds, the email is skipped), and identical responses whether or not an email was sent. A Cloudflare Turnstile check is held in reserve (feature flag) in case abuse appears; it needs a single shared verification endpoint because custom domains exceed Turnstile's per-widget hostname limit.
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
5. Join: logged in → one click; otherwise email → `join_group` magic link returning to the group page. Enforce `member_limit` (NULL = unlimited) inside the join transaction after `SELECT … FROM groups WHERE id = $1 FOR UPDATE`, so two simultaneous joins can't both take the last place. The join fragment shows "This group is full" at the limit.
6. Leave: removes the membership and preferences. An owner must transfer ownership first.
7. Account page: display name; delete account (blocked while the user owns a group; deletes their posts, un-links RSVPs).
8. Nav and join fragments.

### Phase 4: Posts, Replies, Uploads & Moderation (4–5 days)

1. Feed posts (members if `members_can_post`, organizers always), replies to feed posts (members, one level), meeting comments (members). Non-members see content and a "Join to take part" prompt.
2. Authors are shown by display name; members with no name show as "Member". Never show emails.
3. Uploads: images (JPEG, PNG, GIF, WebP) and documents (PDF, DOC, DOCX), max 10 MB each, validated by sniffed content type. Before storing, check `SUM(size_bytes)` for the group plus the new files against `storage_limit_bytes`. Keys: `groups/{group_id}/{uuidv7}{ext}`.
4. The bucket is private. Files are served by `GET /files/{group_id}/{name}`, which streams from S3 on the visitor's own hostname with an immutable one-year cache header, so the CDN caches them and a custom domain never exposes an AWS hostname. Group pages are public, so no per-request authorization is needed. Cover images and logos use the same route.
5. Delete (author, group organizer, platform admin): delete S3 objects for the post and its replies' attachments first, then the rows; purge the affected `/files/...` URLs.
6. HTMX: new posts are inserted without a page reload; purge affected pages.

### Phase 4b: Looks & Palette (4–5 days)

Looks are free for every group, so they ship before billing. The owner settings page (`/admin/groups/{slug}/settings`) is created here with a Look section; Phase 10 adds plan, branding and domain sections to it.

1. `branding/looks.go`: the allowlist (`clean`, `lively`, `confetti`, `poster`).
2. `branding/palette.go`: turns a validated hex into text-on-brand, a text-safe brand color for links, three derived accents, and four light and four dark tints (exact math in `design.md`). Emits explicit hex values, so no browser needs CSS relative-color support. Table-driven tests for pure grays, very light and very dark colors, and marigold-like yellows; assert that every emitted text/background pair meets its contrast target.
3. Palette in the page head from the group's color, or from the platform default blue when the group has none.
4. Look CSS scoped under `[data-look="…"]`, and the look-specific partials (`meeting_header`, `rsvp_going`, `group_header`) for Lively, Confetti and Poster. Self-hosted fonts for the looks that need them; `confetti.js` for Confetti.
5. Look picker: four cards, each a small preview in the group's current color. `POST /admin/groups/{slug}/look` (owner) saves it and purges the group's pages on every hostname.
6. Check each look against long content (60-character title, unbroken group name, 5-line description), in light and dark, with reduced motion on, and at 320 / 768 / 1024 px.

### Phase 5: Co-organizers & Ownership (2–3 days)

1. Promote and demote (owner).
2. Invite by email (owner): always sends an `invite_coorganizer` magic link, even to existing members. Nothing changes until the recipient accepts.
3. Remove members: co-organizers can remove `member`s; the owner can remove anyone but themselves.
4. Transfer ownership (owner): one transaction swaps roles. If the group is paid, set the Stripe subscription to cancel at period end (the group keeps paid features until then) and email the new owner a link to subscribe. Their Checkout session sets `trial_end` to the old period's end, so the first charge happens then and nobody pays twice.
5. Write a table-driven test covering every role × action in the route table.

### Phase 6: Email Notifications (4–5 days)

1. **SES sender** (`email/ses.go`) behind the `Sender` interface from Phase 0. Every notification email carries `List-Unsubscribe` and `List-Unsubscribe-Post: List-Unsubscribe=One-Click` headers pointing at a signed `/unsubscribe/{token}` URL, passed as custom `Headers` (never `ListManagementOptions`). The token identifies either (user, group, type) or, for anonymous RSVPers, (email, group). Non-ASCII group names in the From display name need RFC 2047 encoding.
2. Unsubscribe: GET shows a confirm page, POST applies it (to `notification_preferences` or `email_opt_outs`).
3. New meeting → members with `new_meeting = true`.
4. Changed or canceled → RSVP'd emails (minus opt-outs) plus members with `meeting_changed = true`. Changing the start time sets `reminder_sent_at = NULL`.
5. Reminders (`jobs/reminders.go`, every 5 minutes): meetings starting within 24 hours with `reminder_sent_at IS NULL` and not canceled. Recipients follow the group's `reminder_audience`: `rsvped` sends to RSVP'd emails only; `all_members` sends to RSVP'd emails plus members with `meeting_reminder = true`. Opt-outs are always excluded. Set `reminder_sent_at` after enqueueing.
6. Virtual links are included only for recipients who RSVP'd.
7. **Two streams.** `auth` (magic links, join and invite links, ownership notices) sends from an address on the `auth.` subdomain with its own SES configuration set, no tracking and no group-authored content. `notify` (announcements, reminders, change notices, organizer messages, RSVP confirmations) sends from the `mail.` subdomain with its own configuration set. The group's name is the display name for group emails. Open and click tracking stay off. Per-group sending domains are deferred.
8. Custom messages: compose form; members with `custom_message = true`; limit 3 per group per day.
9. Preferences page: four HTMX toggles per group.
10. **Outbox fan-out.** Announcements and custom messages enqueue one outbox row per recipient (each has its own unsubscribe link). The poller drains `notify` rows at a capped rate (start around 5 per second, within the SES quota) so a 2,000-message burst from a young domain spreads over several minutes. `auth` rows are picked up first and never wait behind a `notify` backlog. A row is marked `sending` before the SES call and `sent` with the SES message ID afterwards; a crash in between can duplicate one email, which is accepted for notifications, and `auth` rows are retried only after a short delay.
11. **Bounces and complaints.** Create an SNS topic for each configuration set's bounce, complaint and delivery events, and subscribe `https://origin…/webhooks/ses`. `email/events.go` verifies the SNS signature, accepts only the configured topic ARN, confirms the subscription, then:
    - hard bounce or complaint → insert into `email_suppressions` (never send again, platform-wide);
    - every event → `email_events`, tied to the group through the outbox row's `group_id`;
    - a group with more than 0.1% complaints over its last 1,000 recipients, or more than 2% hard bounces, gets `email_paused_at` set: notification emails to its members stop, login mail still works, and the owner and the platform admin are emailed.
    SES reports only hard bounces and complaints that reach it, and Gmail spam-button complaints are not reported, so also keep unsubscribe easy.
12. `guard.go` runs before every send: suppression list, opt-outs, group pause (notify stream only), and a global cap that stops the poller if the account's own bounce rate approaches SES's review thresholds (5% bounce, 0.1% complaint).

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

1. **Spike first (half a day):** set up a test pull zone with the origin at `origin.yourcommunity.com` and one test custom hostname. Determine how the app learns the visitor's hostname: with Forward Host Header on, does Bunny connect using the origin's name for TLS while sending the visitor's hostname as `Host`, and what does Caddy need to accept it? Finalize the Caddyfile and set `HOST_SOURCE` from the result. In the same spike, confirm that the pull zone's cache key includes the hostname (request `/` on the main domain and on the test hostname and check they don't share an entry), that `Set-Cookie` and `no-store` responses are not cached, and that a Bunny URL purge for a custom-domain URL works.
2. DNS: `yourcommunity.com` → CNAME to the pull zone (or Bunny DNS if you want the apex); `origin.yourcommunity.com` → the EC2 instance's Elastic IP.
3. Cache headers per route category (including private pages and `/files/*`, see Route Definitions); `/sw.js` short-lived; `/static/*` immutable.
4. Implement `cdn/bunny.go` and `PurgePaths`; wire every mutation.
5. Verify that no cacheable response ever contains emails or virtual links (add a test that renders public pages and greps for `@` and `virtual_url` values).
6. Log purge failures.

### Phase 10: Paid Tier & Custom Domains (4–5 days)

1. **Stripe:**
   - One product and a $20/year price. `POST /upgrade` creates a Checkout session with `group_id` and the payer's user ID in metadata.
   - Webhooks (`checkout.session.completed`, `customer.subscription.updated`, `customer.subscription.deleted`, `invoice.payment_failed`) are processed idempotently by event ID (`stripe_events`).
   - Handle every Stripe subscription status in the schema (`trialing`, `active`, `past_due`, `unpaid`, `incomplete`, `canceled`). A group has paid features while any of its subscriptions is `trialing`, `active` or `past_due`.
   - Upgrade is offered when the group has no renewing subscription (free, or paid with an ending subscription after an ownership transfer). In the transfer case, set `trial_end` to the ending subscription's `current_period_end`.
   - Decide tax handling (Stripe Tax or none) before launch; see `decisions.md`.
   - The success page shows "Processing…" until the webhook has updated the tier.
2. **Going paid:** `tier='paid'`, `member_limit=NULL`, `storage_limit_bytes=25 GB`.
3. **Lapse (subscription deleted or past period end):**
   - Set `tier='free'`, `member_limit=150`, `storage_limit_bytes=2 GB`, `branding='{}'` (color and logo revert to the platform default; the group keeps its look).
   - Remove the custom domain: Bunny `RemoveHostname`, remove it from the Google browser key's allowed websites, clear the columns, refresh the site cache.
   - Purge the group's pages.
4. **Customer portal** link for card updates and cancellation.
5. **Branding:** validated hex color (the palette computation is built in Phase 4b), logo upload (image, max 2 MB; cover images and logos don't count toward storage). Logo and group name replace platform identity on the custom domain. The color and logo fields on the settings page are locked for free groups, with an upgrade link. Saving purges the group's pages on every hostname.
6. **Custom domain lifecycle:**
   - `POST /domain`: validate the format (not an IP, must have a dot, not our own domains, not already claimed), call `AddHostname`, set `pending_dns`, and show the CNAME instructions (warn if it's an apex domain).
   - `POST /domain/verify`: resolve the CNAME to confirm it points at the pull zone.
   - Once it does: call `LoadFreeCertificate`, add the domain to the Google browser key's allowed websites (manually at first; automatable with Google's API Keys API), set `active`, refresh the site cache.
   - `DELETE /domain` reverses all of it.
7. **White-label checks on a custom domain:**
   - Base layout, email content, manifest, error pages, and legal pages show no platform name or links (the sender address on the shared domain is the accepted v1 exception).
   - Platform-only routes 404.
   - Login and admin work end to end on the custom domain (the host-bound sessions from Phase 3).
8. **Delete group:** cancel the Stripe subscription, remove the custom domain, delete every S3 object under `groups/{group_id}/`, delete the group row (cascades), purge.

### Phase 11: PWA (1–2 days)

1. `GET /manifest.json` generated per host: platform name and icons on the main domain; group name, logo, and brand color on custom domains (the look does not affect it).
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
4. **Email DNS and SES readiness:** DKIM (SES Easy DKIM), custom MAIL FROM, SPF and DMARC for both `auth.` and `mail.`; production access granted; SNS topics subscribed and confirmed; Gmail Postmaster Tools set up for both subdomains. Send a real message to Gmail and check `Authentication-Results`, and that the DKIM `h=` list includes `List-Unsubscribe` and `List-Unsubscribe-Post`. Measure magic-link delivery latency.
5. **Hardening:**
   - Error pages (404, 500).
   - Rate limits: magic links 3 per email per 15 minutes; RSVPs 10 per IP per minute; custom messages 3 per group per day.
   - Input validation; structured `slog` logging.
6. **Provision:** EC2 t4g.small in one region (same region as SES) with an Elastic IP; a separate EBS volume for Postgres data; a security group allowing 22 (your IP only), 80 and 443 (SNS and Stripe call the origin directly, so 80/443 can't be limited to Bunny's edge IPs unless webhooks are moved elsewhere); the IAM instance role from the Docker section; a private S3 bucket for uploads with all public access blocked (no CORS: uploads go through the app) and a second private bucket for backups.
7. **Deploy:** DNS per Phase 9; Bunny pull zone → `origin.yourcommunity.com`; `make deploy`.
8. **Backups:** nightly `pg_dump` uploaded to the private backups bucket with a 14-day lifecycle rule, plus daily EBS snapshots of the data volume; test a restore once. Postgres is on the same instance, so there is no high availability and no point-in-time restore; the worst case is losing up to a day of data. Revisit WAL archiving or RDS if that stops being acceptable (see `decisions.md`).
9. **Monitoring:** CloudWatch alarms (CPU, memory via the agent, disk, instance status check) and SES account alarms (bounce and complaint rates, sending quota); log-based check that the reminder, cleanup and outbox jobs run; an alert when `email_outbox` has old unsent rows.
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
