-- 001_initial_schema.down.sql
DROP TABLE IF EXISTS email_events, email_suppressions, email_outbox, stripe_events,
    subscriptions, email_opt_outs, notification_preferences, attachments,
    posts, rsvps, meetings, memberships, groups, magic_links, sessions, users;
