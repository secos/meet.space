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
-- 'sending' rows are included so a row whose sender crashed mid-send is
-- found again once its 2-minute claim (next_attempt_at, set when the row
-- is taken) runs out (decisions.md "Outbox rows stuck in sending").
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
