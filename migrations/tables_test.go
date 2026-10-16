package migrations_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"

	"meet.space/internal/branding"
	"meet.space/internal/testkit"
)

// wantValues checks a column's CHECK: each accepted value inserts, and each
// refused value fails on the named constraint.
func wantValues(t *testing.T, f *fx, table, col, constraint string, accepted, refused []any) {
	t.Helper()
	for _, v := range accepted {
		if err := f.insert(table, f.row(table, col, v)); err != nil {
			t.Errorf("%s.%s = %#v refused: %v", table, col, v, err)
		}
	}
	for _, v := range refused {
		if !testkit.WantPgError(t, f.insert(table, f.row(table, col, v)), pgerrcode.CheckViolation, constraint) {
			t.Errorf("  (value %#v)", v)
		}
	}
}

// decisions.md "No triggers": the schema enforces its rules with
// constraints and keys only. Postgres's own internal triggers (those that
// implement foreign keys) don't count.
func TestSchema_HasNoTriggers(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	rows, err := f.q.Query(f.ctx, `SELECT c.relname || '.' || tg.tgname FROM pg_trigger tg
		JOIN pg_class c ON c.oid = tg.tgrelid
		WHERE NOT tg.tgisinternal AND c.relnamespace = 'public'::regnamespace`)
	if err != nil {
		t.Fatal(err)
	}
	triggers, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(triggers) != 0 {
		t.Errorf("triggers in the schema: %v", triggers)
	}
}

// ---- magic_links ---------------------------------------------------------

// P0-SCHEMA-14
// Security Rule 3 (magic links), database half: every magic link records
// the hostname it must be verified on; a link without one is refused.
func TestRule3_MagicLinkHostIsRequired(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	testkit.WantPgError(t, f.insert("magic_links", f.row("magic_links", "host", nil)), pgerrcode.NotNullViolation, "host")
	testkit.WantPgError(t, f.insert("magic_links", f.row("magic_links").without("host")), pgerrcode.NotNullViolation, "host")
	if err := f.insert("magic_links", f.row("magic_links", "host", "hikers.localhost:8080")); err != nil {
		t.Errorf("link for a custom domain refused: %v", err)
	}
}

// P0-SCHEMA-14
// A magic link's token hash is unique, so a token finds at most one link.
func TestMagicLinks_TokenHashIsUnique(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	f.add("magic_links", "token_hash", "same")
	testkit.WantPgError(t, f.insert("magic_links", f.row("magic_links", "token_hash", "same")), pgerrcode.UniqueViolation, "magic_links_token_hash_key")
}

// P0-SCHEMA-14
// A magic link's purpose is log in, join a group or accept a co-organizer
// invite, and defaults to log in.
func TestMagicLinks_PurposeIsOneOfThreeAndDefaultsToAuth(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	wantValues(t, f, "magic_links", "purpose", "magic_links_purpose_check",
		[]any{"auth", "join_group", "invite_coorganizer"},
		[]any{"", "login", "Auth", "invite_co_organizer", "transfer_ownership"})
	stored, _ := f.inserted("magic_links", f.row("magic_links"))
	wantStored(t, stored, map[string]any{"purpose": "auth", "metadata": nil, "redirect_path": nil, "used_at": nil})
}

// P0-SCHEMA-14
// Requesting several links leaves several unused links for the same email
// and host side by side (workflows.md §3: each works independently until
// used or expired).
func TestMagicLinks_SeveralUnusedLinksForOneEmailCoexist(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	for range 3 {
		f.add("magic_links", "email", "ann@example.com", "host", "localhost:8080")
	}
	if n := f.count(`SELECT count(*) FROM magic_links WHERE email = 'ann@example.com' AND used_at IS NULL`); n != 3 {
		t.Errorf("%d unused links, want 3", n)
	}
}

// ---- sessions ------------------------------------------------------------

// P0-SCHEMA-15
// Security Rule 4 (sessions), database half: every session records the
// hostname it is honored on; a session without one is refused.
func TestRule4_SessionHostIsRequired(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	testkit.WantPgError(t, f.insert("sessions", f.row("sessions", "host", nil)), pgerrcode.NotNullViolation, "host")
	testkit.WantPgError(t, f.insert("sessions", f.row("sessions").without("host")), pgerrcode.NotNullViolation, "host")
}

// P0-SCHEMA-15
// A session's token hash is unique, so a cookie finds at most one session.
func TestSessions_TokenHashIsUnique(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	f.add("sessions", "token_hash", "same")
	testkit.WantPgError(t, f.insert("sessions", f.row("sessions", "token_hash", "same")), pgerrcode.UniqueViolation, "sessions_token_hash_key")
}

// P0-SCHEMA-15
// One user holds separate sessions on the platform and on custom domains
// (workflows.md §3: logging in on a custom domain is a second login), and
// deleting the user ends every one of them (§14: "deleting the account ends
// every session on every domain").
func TestSessions_OneUserHasSessionsOnManyHostsAndDeletionEndsAll(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	u := f.user()
	other := f.user()
	for _, host := range []string{"localhost:8080", "hikers.localhost:8080", "chess.localhost:8080"} {
		f.add("sessions", "user_id", u, "host", host)
	}
	f.add("sessions", "user_id", other)
	if n := f.count(`SELECT count(DISTINCT host) FROM sessions WHERE user_id = $1`, u); n != 3 {
		t.Fatalf("user has sessions on %d hosts, want 3", n)
	}
	f.exec(`DELETE FROM users WHERE id = $1`, u)
	if n := f.count(`SELECT count(*) FROM sessions WHERE user_id = $1`, u); n != 0 {
		t.Errorf("%d sessions survive the user's deletion", n)
	}
	if n := f.count(`SELECT count(*) FROM sessions WHERE user_id = $1`, other); n != 1 {
		t.Errorf("another user's session was removed")
	}
}

// ---- users ---------------------------------------------------------------

// P0-SCHEMA-16
// A user is found by a unique, lowercase email; the display name defaults
// to empty, the platform-admin flag to false, and both timestamps to the
// time of the insert.
func TestUsers_UniqueEmailAndDefaults(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	f.add("users", "email", "ann@example.com")
	testkit.WantPgError(t, f.insert("users", f.row("users", "email", "ann@example.com")), pgerrcode.UniqueViolation, "users_email_key")
	testkit.WantPgError(t, f.insert("users", f.row("users", "email", "Ann@example.com")), pgerrcode.CheckViolation, "users_email_check")

	stored, now := f.inserted("users", f.row("users"))
	wantStored(t, stored, map[string]any{"name": "", "is_platform_admin": false, "created_at": now, "updated_at": now})
}

// ---- groups --------------------------------------------------------------

// P0-SCHEMA-17
// A group created with only its slug, name and timezone gets the plan's
// defaults: free tier, 150 members, 2 GB of storage, the Clean look, empty
// branding, members may post, reminders to RSVPers, no custom domain and
// not suspended or paused.
func TestGroups_Defaults(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	stored, now := f.inserted("groups", f.row("groups"))
	wantStored(t, stored, map[string]any{
		"description":          "",
		"topic":                "",
		"location":             "",
		"latitude":             nil,
		"longitude":            nil,
		"cover_image_key":      nil,
		"members_can_post":     true,
		"reminder_audience":    "rsvped",
		"tier":                 "free",
		"member_limit":         float64(150),
		"storage_limit_bytes":  float64(2147483648),
		"custom_domain":        nil,
		"custom_domain_status": nil,
		"look":                 "clean",
		"branding":             map[string]any{},
		"suspended_at":         nil,
		"email_paused_at":      nil,
		"created_at":           now,
		"updated_at":           now,
	})
}

// P0-SCHEMA-18
// Group CHECKs and uniques: tier is free or paid; reminder audience is
// RSVPers or all members; a custom domain's status is only pending_dns or
// active; the slug is unique; a custom domain belongs to one group while
// any number of groups have none; the member limit may be NULL (unlimited,
// paid).
func TestGroups_ChecksAndUniques(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	t.Run("tier", func(t *testing.T) {
		wantValues(t, f.on(t), "groups", "tier", "groups_tier_check", []any{"free", "paid"}, []any{"", "pro", "Paid"})
	})
	t.Run("reminder_audience", func(t *testing.T) {
		wantValues(t, f.on(t), "groups", "reminder_audience", "groups_reminder_audience_check",
			[]any{"rsvped", "all_members"}, []any{"", "everyone", "members"})
	})
	t.Run("custom_domain_status", func(t *testing.T) {
		wantValues(t, f.on(t), "groups", "custom_domain_status", "groups_custom_domain_status_check",
			[]any{nil, "pending_dns", "active"}, []any{"", "pending", "removed", "failed", "Active"})
	})
	t.Run("slug unique", func(t *testing.T) {
		f := f.on(t)
		f.add("groups", "slug", "hikers")
		testkit.WantPgError(t, f.insert("groups", f.row("groups", "slug", "hikers")), pgerrcode.UniqueViolation, "groups_slug_key")
	})
	t.Run("custom_domain unique, many NULL", func(t *testing.T) {
		f := f.on(t)
		f.add("groups", "custom_domain", "hikers.example.com")
		testkit.WantPgError(t, f.insert("groups", f.row("groups", "custom_domain", "hikers.example.com")), pgerrcode.UniqueViolation, "groups_custom_domain_key")
		for range 3 {
			f.add("groups", "custom_domain", nil)
		}
	})
	t.Run("member_limit nullable", func(t *testing.T) {
		f := f.on(t)
		f.add("groups", "member_limit", nil, "tier", "paid")
	})
}

// P0-SCHEMA-19
// Security Rule 10 (branding values), database half: the look is one of
// the four allowlisted names, enforced by a CHECK. The database and the
// handler's allowlist (branding.ValidLook) agree on every candidate,
// including the dropped Noticeboard and Program looks and near misses.
func TestRule10_LookCheckAllowsOnlyTheFourLooks(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	candidates := append(slices.Clone(branding.Looks),
		"noticeboard", "program", "Clean", "CLEAN", "", " clean", "clean ", "clean;")
	accepted := 0
	for _, look := range candidates {
		t.Run(fmt.Sprintf("%q", look), func(t *testing.T) {
			f := f.on(t)
			err := f.insert("groups", f.row("groups", "look", look))
			if branding.ValidLook(look) {
				accepted++
				if err != nil {
					t.Errorf("allowlisted look refused: %v", err)
				}
				return
			}
			testkit.WantPgError(t, err, pgerrcode.CheckViolation, "groups_look_check")
		})
	}
	if want := []string{"clean", "lively", "confetti", "poster"}; !slices.Equal(branding.Looks, want) {
		t.Errorf("branding.Looks = %v, want the plan's %v", branding.Looks, want)
	}
	if accepted != 4 {
		t.Errorf("%d candidates accepted, want the 4 looks", accepted)
	}
}

// P0-SCHEMA-20
// search_vector is generated from name and topic (weight A) and description
// (weight B) with English stemming, follows every UPDATE, and can't be
// written directly.
func TestGroups_SearchVectorIsGenerated(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	id := f.group("name", "Trail Hikers", "topic", "Outdoors", "description", "Weekend walking club")
	vector := func() string {
		var v string
		f.scan(`SELECT search_vector::text FROM groups WHERE id = $1`, []any{id}, &v)
		return v
	}
	matches := func(q string) bool {
		var ok bool
		f.scan(`SELECT search_vector @@ websearch_to_tsquery('english', $2) FROM groups WHERE id = $1`, []any{id, q}, &ok)
		return ok
	}
	if got, want := vector(), `'club':6B 'hiker':2A 'outdoor':3A 'trail':1A 'walk':5B 'weekend':4B`; got != want {
		t.Errorf("search_vector = %s\nwant           %s", got, want)
	}
	for _, q := range []string{"hiker", "Hikers", "outdoor", "walks", "walked", "clubs"} {
		if !matches(q) {
			t.Errorf("%q does not match after stemming", q)
		}
	}

	f.exec(`UPDATE groups SET name = 'Board Gamers', topic = 'Games', description = 'Monthly chess evenings' WHERE id = $1`, id)
	if got, want := vector(), `'board':1A 'chess':5B 'even':6B 'game':3A 'gamer':2A 'month':4B`; got != want {
		t.Errorf("after update search_vector = %s\nwant                        %s", got, want)
	}
	if matches("hikers") || !matches("chess") {
		t.Error("search_vector did not follow the update")
	}

	testkit.WantPgError(t, f.try(`INSERT INTO groups (id, slug, name, timezone, search_vector) VALUES ($1, 'direct', 'n', 'UTC', to_tsvector('x'))`, newID(t)),
		pgerrcode.GeneratedAlways, "search_vector")
	testkit.WantPgError(t, f.try(`UPDATE groups SET search_vector = to_tsvector('x') WHERE id = $1`, id),
		pgerrcode.GeneratedAlways, "search_vector")
}

// ---- memberships ---------------------------------------------------------

// P0-SCHEMA-21
// A membership's role is owner, co_organizer or member and defaults to
// member; a user has at most one membership per group, and a join written
// as ON CONFLICT DO NOTHING changes nothing the second time.
func TestMemberships_RoleUniqueAndOnConflict(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	wantValues(t, f, "memberships", "role", "memberships_role_check",
		[]any{"owner", "co_organizer", "member"}, []any{"", "admin", "Owner", "co-organizer", "organizer"})
	stored, now := f.inserted("memberships", f.row("memberships"))
	wantStored(t, stored, map[string]any{"role": "member", "joined_at": now})

	u, g := f.user(), f.group()
	first := f.member(u, g, "co_organizer")
	testkit.WantPgError(t, f.insert("memberships", f.row("memberships", "user_id", u, "group_id", g)), pgerrcode.UniqueViolation, "memberships_user_id_group_id_key")
	n := f.exec(`INSERT INTO memberships (id, user_id, group_id) VALUES ($1, $2, $3) ON CONFLICT (user_id, group_id) DO NOTHING`, newID(t), u, g)
	if n != 0 {
		t.Errorf("ON CONFLICT DO NOTHING affected %d rows, want 0", n)
	}
	var id uuid.UUID
	var role string
	f.scan(`SELECT id, role FROM memberships WHERE user_id = $1 AND group_id = $2`, []any{u, g}, &id, &role)
	if id != first || role != "co_organizer" {
		t.Errorf("membership became %s/%s, want the original %s/co_organizer", id, role, first)
	}
	f.member(u, f.group(), "member") // the same user in another group
}

// P0-SCHEMA-22
// A group has at most one owner: a second owner is refused whether it is
// inserted or promoted by UPDATE, while other groups keep their own owners
// and a group has any number of co-organizers and members. At least one
// owner is the server's job, not the database's (decisions.md "One owner
// per group"); it is tested where groups are created, transferred, left
// and deleted.
func TestMemberships_AtMostOneOwnerPerGroup(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	g := f.group()
	f.member(f.user(), g, "owner")
	testkit.WantPgError(t, f.insert("memberships", f.row("memberships", "group_id", g, "role", "owner")), pgerrcode.UniqueViolation, "idx_one_owner_per_group")
	m := f.member(f.user(), g, "co_organizer")
	f.member(f.user(), g, "co_organizer")
	f.member(f.user(), g, "member")
	testkit.WantPgError(t, f.try(`UPDATE memberships SET role = 'owner' WHERE id = $1`, m), pgerrcode.UniqueViolation, "idx_one_owner_per_group")
	f.member(f.user(), f.group(), "owner")
	if n := f.count(`SELECT count(*) FROM memberships WHERE group_id = $1 AND role = 'owner'`, g); n != 1 {
		t.Errorf("group has %d owners, want 1", n)
	}
}

// P0-SCHEMA-22
// Two transactions making different users owner of the same group at the
// same moment: exactly one commits and the other is refused by the
// one-owner index, by insert and by promotion.
func TestMemberships_ConcurrentOwnersOnlyOneWins(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	check := func(t *testing.T, errs []error, g uuid.UUID) {
		t.Helper()
		wins := 0
		for _, err := range errs {
			if err == nil {
				wins++
				continue
			}
			testkit.WantPgError(t, err, pgerrcode.UniqueViolation, "idx_one_owner_per_group")
		}
		if wins != 1 {
			t.Errorf("%d owners committed, want exactly 1", wins)
		}
		if n := f.on(t).count(`SELECT count(*) FROM memberships WHERE group_id = $1 AND role = 'owner'`, g); n != 1 {
			t.Errorf("group has %d owners, want 1", n)
		}
	}
	t.Run("insert", func(t *testing.T) {
		f := f.on(t)
		g := f.group()
		users := []uuid.UUID{f.user(), f.user()}
		ids := []uuid.UUID{newID(t), newID(t)}
		errs := testkit.Race(t, f.q, 2, func(ctx context.Context, i int, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO memberships (id, user_id, group_id, role) VALUES ($1, $2, $3, 'owner')`, ids[i], users[i], g)
			return err
		})
		check(t, errs, g)
	})
	t.Run("promote", func(t *testing.T) {
		f := f.on(t)
		g := f.group()
		ms := []uuid.UUID{f.member(f.user(), g, "co_organizer"), f.member(f.user(), g, "member")}
		errs := testkit.Race(t, f.q, 2, func(ctx context.Context, i int, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE memberships SET role = 'owner' WHERE id = $1`, ms[i])
			return err
		})
		check(t, errs, g)
	})
}

// P0-SCHEMA-23
// Ownership transfer in one transaction (workflows.md §9: owner →
// co_organizer, then target → owner) succeeds. Promoting the target first
// fails on the one-owner index, and rolling that back leaves the original
// owner in place.
func TestMemberships_OwnershipTransferDemotesBeforePromoting(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	roles := func(f *fx, g uuid.UUID) map[uuid.UUID]string {
		t := f.t
		rows, err := f.q.Query(f.ctx, `SELECT user_id, role FROM memberships WHERE group_id = $1`, g)
		if err != nil {
			t.Fatal(err)
		}
		out := map[uuid.UUID]string{}
		for rows.Next() {
			var u uuid.UUID
			var r string
			if err := rows.Scan(&u, &r); err != nil {
				t.Fatal(err)
			}
			out[u] = r
		}
		return out
	}
	t.Run("demote then promote", func(t *testing.T) {
		f := f.on(t)
		g, owner, target := f.group(), f.user(), f.user()
		f.member(owner, g, "owner")
		f.member(target, g, "member")
		tx, err := f.q.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(f.ctx) //nolint:errcheck
		for _, step := range []struct {
			user uuid.UUID
			role string
		}{{owner, "co_organizer"}, {target, "owner"}} {
			if _, err := tx.Exec(f.ctx, `UPDATE memberships SET role = $1 WHERE group_id = $2 AND user_id = $3`, step.role, g, step.user); err != nil {
				t.Fatalf("set %s: %v", step.role, err)
			}
		}
		if err := tx.Commit(f.ctx); err != nil {
			t.Fatal(err)
		}
		if got := roles(f, g); got[owner] != "co_organizer" || got[target] != "owner" {
			t.Errorf("after transfer roles are %v", got)
		}
	})
	t.Run("promote first fails and rolls back", func(t *testing.T) {
		f := f.on(t)
		g, owner, target := f.group(), f.user(), f.user()
		f.member(owner, g, "owner")
		f.member(target, g, "co_organizer")
		tx, err := f.q.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(f.ctx, `UPDATE memberships SET role = 'owner' WHERE group_id = $1 AND user_id = $2`, g, target)
		testkit.WantPgError(t, err, pgerrcode.UniqueViolation, "idx_one_owner_per_group")
		if err := tx.Rollback(f.ctx); err != nil {
			t.Fatal(err)
		}
		if got := roles(f, g); got[owner] != "owner" || got[target] != "co_organizer" {
			t.Errorf("after rollback roles are %v, want the original owner kept", got)
		}
	})
}

// ---- meetings ------------------------------------------------------------

// P0-SCHEMA-24
// A meeting defaults to 60 minutes, in person, not canceled, with no
// reminder sent and no virtual link; its duration must be positive and its
// type is in_person or virtual.
func TestMeetings_DefaultsAndChecks(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	stored, now := f.inserted("meetings", f.row("meetings"))
	wantStored(t, stored, map[string]any{
		"description":      "",
		"duration_min":     float64(60),
		"meeting_type":     "in_person",
		"location":         "",
		"place_id":         nil,
		"latitude":         nil,
		"longitude":        nil,
		"virtual_url":      nil,
		"canceled":         false,
		"reminder_sent_at": nil,
		"created_at":       now,
		"updated_at":       now,
	})
	wantValues(t, f, "meetings", "duration_min", "meetings_duration_min_check", []any{1, 90, 600}, []any{0, -30})
	wantValues(t, f, "meetings", "meeting_type", "meetings_meeting_type_check",
		[]any{"in_person", "virtual"}, []any{"", "hybrid", "online", "Virtual"})
}

// ---- rsvps ---------------------------------------------------------------

// P0-SCHEMA-25
// One RSVP per email per meeting. A duplicate written as ON CONFLICT DO
// NOTHING (workflows.md §2: "Duplicate RSVP: treated as success") changes
// nothing and raises no error, both one after another and when several
// arrive at the same moment; the same email may RSVP to other meetings.
func TestRSVPs_OnePerEmailPerMeeting(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	const upsert = `INSERT INTO rsvps (id, meeting_id, email, name) VALUES ($1, $2, $3, $4) ON CONFLICT (meeting_id, email) DO NOTHING`
	t.Run("plain duplicate refused", func(t *testing.T) {
		f := f.on(t)
		m := f.meeting(f.group())
		f.add("rsvps", "meeting_id", m, "email", "ann@example.com")
		testkit.WantPgError(t, f.insert("rsvps", f.row("rsvps", "meeting_id", m, "email", "ann@example.com")), pgerrcode.UniqueViolation, "rsvps_meeting_id_email_key")
		f.add("rsvps", "meeting_id", f.meeting(f.group()), "email", "ann@example.com")
	})
	t.Run("sequential ON CONFLICT", func(t *testing.T) {
		f := f.on(t)
		m := f.meeting(f.group())
		first := newID(t)
		if n := f.exec(upsert, first, m, "ann@example.com", "Ann"); n != 1 {
			t.Fatalf("first RSVP inserted %d rows", n)
		}
		if n := f.exec(upsert, newID(t), m, "ann@example.com", "Someone else"); n != 0 {
			t.Errorf("duplicate RSVP inserted %d rows, want 0", n)
		}
		var id uuid.UUID
		var name string
		f.scan(`SELECT id, name FROM rsvps WHERE meeting_id = $1`, []any{m}, &id, &name)
		if id != first || name != "Ann" {
			t.Errorf("RSVP is now %s/%q, want the first one kept", id, name)
		}
	})
	t.Run("racing ON CONFLICT", func(t *testing.T) {
		f := f.on(t)
		m := f.meeting(f.group())
		const n = 4
		affected := make([]int64, n)
		ids := make([]uuid.UUID, n)
		for i := range ids {
			ids[i] = newID(t)
		}
		errs := testkit.Race(t, f.q, n, func(ctx context.Context, i int, tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, upsert, ids[i], m, "ann@example.com", fmt.Sprintf("Ann %d", i))
			affected[i] = tag.RowsAffected()
			return err
		})
		var total int64
		for i, err := range errs {
			if err != nil {
				t.Errorf("contender %d: %v", i, err)
			}
			total += affected[i]
		}
		if total != 1 {
			t.Errorf("%d contenders inserted, want exactly 1", total)
		}
		if c := f.count(`SELECT count(*) FROM rsvps WHERE meeting_id = $1`, m); c != 1 {
			t.Errorf("%d RSVP rows, want 1", c)
		}
	})
}

// P0-SCHEMA-25
// Deleting a user keeps their RSVPs (so headcounts don't change) with the
// email in place and the user link cleared (workflows.md §14: "RSVPs keep
// email, user_id → NULL").
// OPEN: §14's screen flow says the RSVPs stay "as anonymous RSVPs (email
// only)", which may mean the RSVP name is cleared too, and decisions.md
// question 5 (erasure policy) is still open. The database leaves the name
// untouched; whether that is wanted is not asserted.
func TestRSVPs_UserDeletionKeepsTheRSVPWithEmail(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	u := f.user("email", "ann@example.com")
	m := f.meeting(f.group())
	r := f.add("rsvps", "meeting_id", m, "email", "ann@example.com", "name", "Ann", "user_id", u).id()
	f.exec(`DELETE FROM users WHERE id = $1`, u)
	var email string
	var userID *uuid.UUID
	f.scan(`SELECT email, user_id FROM rsvps WHERE id = $1`, []any{r}, &email, &userID)
	if email != "ann@example.com" || userID != nil {
		t.Errorf("RSVP after user deletion: email %q user_id %v, want ann@example.com and NULL", email, userID)
	}
}

// ---- posts ---------------------------------------------------------------

// P0-SCHEMA-26
// A post's content is never empty, and a post is a feed post, a reply to a
// feed post, or a meeting comment, never both a reply and a comment.
func TestPosts_ContentAndShape(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	g := f.group()
	m := f.meeting(g)
	feed := f.post(g)
	testkit.WantPgError(t, f.insert("posts", f.row("posts", "content", "")), pgerrcode.CheckViolation, "posts_content_check")
	f.add("posts", "content", "x")
	for name, shape := range map[string][]any{
		"feed post":       {"group_id", g},
		"reply":           {"group_id", g, "parent_id", feed},
		"meeting comment": {"group_id", g, "meeting_id", m},
	} {
		if err := f.insert("posts", f.row("posts", shape...)); err != nil {
			t.Errorf("%s refused: %v", name, err)
		}
	}
	testkit.WantPgError(t, f.insert("posts", f.row("posts", "group_id", g, "parent_id", feed, "meeting_id", m)), pgerrcode.CheckViolation, "posts_check")
}

// workflows.md §7 ("Reply to a reply, or reply on a meeting comment: not
// supported (rejected by the server and the database)"; a reply's parent
// "must be a feed post in this group") and decisions.md "Reply rules in the
// database": the database refuses any reply whose parent is not a feed
// post in the reply's own group, and any comment on another group's
// meeting, on insert and on update.
func TestReplies_DatabaseEnforcesTheReplyRules(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	g, other := f.group(), f.group()
	feed := f.post(g)
	reply := f.post(g, "parent_id", feed)
	m := f.meeting(g)
	comment := f.post(g, "meeting_id", m)
	if err := f.insert("posts", f.row("posts", "group_id", g, "parent_id", feed)); err != nil {
		t.Errorf("second reply to a feed post refused: %v", err)
	}
	if err := f.insert("posts", f.row("posts", "group_id", g, "meeting_id", m)); err != nil {
		t.Errorf("comment on the group's own meeting refused: %v", err)
	}

	for name, shape := range map[string][]any{
		"reply to a reply":                {"group_id", g, "parent_id", reply},
		"reply to a meeting comment":      {"group_id", g, "parent_id", comment},
		"reply to another group's post":   {"group_id", other, "parent_id", feed},
		"reply to another group's reply":  {"group_id", other, "parent_id", reply},
		"reply to a post that is missing": {"group_id", g, "parent_id", newID(t)},
	} {
		if !testkit.WantPgError(t, f.insert("posts", f.row("posts", shape...)), pgerrcode.ForeignKeyViolation, "posts_parent_fkey") {
			t.Errorf("  (%s)", name)
		}
	}
	testkit.WantPgError(t, f.insert("posts", f.row("posts", "group_id", other, "meeting_id", m)),
		pgerrcode.ForeignKeyViolation, "posts_meeting_fkey")

	// Updates that would break the rules are refused too.
	testkit.WantPgError(t, f.try(`UPDATE posts SET meeting_id = $1 WHERE id = $2`, m, feed),
		pgerrcode.ForeignKeyViolation, "posts_parent_fkey") // a feed post with replies can't become a comment
	testkit.WantPgError(t, f.try(`UPDATE posts SET group_id = $1 WHERE id = $2`, other, reply),
		pgerrcode.ForeignKeyViolation, "posts_parent_fkey")
	testkit.WantPgError(t, f.try(`UPDATE posts SET parent_id = $1 WHERE id = $2`, reply, f.post(g)),
		pgerrcode.ForeignKeyViolation, "posts_parent_fkey")
	testkit.WantPgError(t, f.try(`UPDATE posts SET group_id = $1 WHERE id = $2`, other, comment),
		pgerrcode.ForeignKeyViolation, "posts_meeting_fkey")

	// Deleting a feed post still deletes its replies.
	f.exec(`DELETE FROM posts WHERE id = $1`, feed)
	if n := f.count(`SELECT count(*) FROM posts WHERE parent_id = $1`, feed); n != 0 {
		t.Errorf("%d replies outlived their feed post", n)
	}
}

// ---- notification_preferences, email_opt_outs ----------------------------

// P0-SCHEMA-27
// A member's four notification toggles default to on, and a user has one
// preferences row per group.
func TestNotificationPreferences_DefaultsOnAndOnePerUserAndGroup(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	u, g := f.user(), f.group()
	stored, _ := f.inserted("notification_preferences", f.row("notification_preferences", "user_id", u, "group_id", g))
	wantStored(t, stored, map[string]any{"new_meeting": true, "meeting_reminder": true, "meeting_changed": true, "custom_message": true})
	testkit.WantPgError(t, f.insert("notification_preferences", f.row("notification_preferences", "user_id", u, "group_id", g)),
		pgerrcode.UniqueViolation, "notification_preferences_user_id_group_id_key")
	f.add("notification_preferences", "user_id", u)
}

// P0-SCHEMA-28
// An anonymous opt-out is one row per email and group; the same email may
// opt out of several groups.
func TestEmailOptOuts_OnePerEmailAndGroup(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	g := f.group()
	stored, now := f.inserted("email_opt_outs", f.row("email_opt_outs", "email", "ann@example.com", "group_id", g))
	wantStored(t, stored, map[string]any{"created_at": now})
	testkit.WantPgError(t, f.insert("email_opt_outs", f.row("email_opt_outs", "email", "ann@example.com", "group_id", g)),
		pgerrcode.UniqueViolation, "email_opt_outs_pkey")
	f.add("email_opt_outs", "email", "ann@example.com")
}

// ---- subscriptions, stripe_events ----------------------------------------

// P0-SCHEMA-29
// A subscription's status is one of the six the plan lists, its Stripe
// subscription id is unique, it renews unless told otherwise, and its
// payer may be unknown.
// OPEN: Stripe also has the statuses 'incomplete_expired' and 'paused',
// which the CHECK refuses; the documents don't say what the webhook stores
// for them, so they are not tested either way.
func TestSubscriptions_StatusAndStripeIDs(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	wantValues(t, f, "subscriptions", "status", "subscriptions_status_check",
		[]any{"trialing", "active", "past_due", "unpaid", "incomplete", "canceled"},
		[]any{"", "Active", "cancelled", "pending", "expired"})
	f.add("subscriptions", "stripe_subscription_id", "sub_1")
	testkit.WantPgError(t, f.insert("subscriptions", f.row("subscriptions", "stripe_subscription_id", "sub_1")),
		pgerrcode.UniqueViolation, "subscriptions_stripe_subscription_id_key")
	stored, now := f.inserted("subscriptions", f.row("subscriptions", "payer_user_id", nil))
	wantStored(t, stored, map[string]any{"cancel_at_period_end": false, "payer_user_id": nil, "created_at": now, "updated_at": now})
}

var subscriptionStatuses = []string{"trialing", "active", "past_due", "unpaid", "incomplete", "canceled"}

// renewing is the plan's definition: a subscription that will bill again.
func renewing(status string, cancelAtPeriodEnd bool) bool {
	return !cancelAtPeriodEnd && slices.Contains([]string{"trialing", "active", "past_due"}, status)
}

// P0-SCHEMA-30
// A group has at most one renewing subscription (trialing, active or
// past_due, and not cancel_at_period_end). Every pair of the 12
// status/cancel combinations is tried in one group: the second row is
// refused exactly when both are renewing. That includes the ownership
// transfer pair (prd.md §7, workflows.md §9): the old owner's active
// subscription set to cancel at period end beside the new owner's trialing
// one. Turning the old one back to renewing while the new one exists is
// refused, and different groups each have their own.
func TestSubscriptions_AtMostOneRenewingPerGroup(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	g := f.group()
	type state struct {
		status string
		cancel bool
	}
	var states []state
	for _, s := range subscriptionStatuses {
		states = append(states, state{s, false}, state{s, true})
	}
	ins := func(f *fx, tx pgx.Tx, s state) error {
		sql, args := insertSQL("subscriptions", f.row("subscriptions", "group_id", g, "status", s.status, "cancel_at_period_end", s.cancel), "")
		_, err := tx.Exec(f.ctx, sql, args...)
		return err
	}
	for i, a := range states {
		for _, b := range states[i:] {
			name := fmt.Sprintf("%s/cancel=%t + %s/cancel=%t", a.status, a.cancel, b.status, b.cancel)
			t.Run(name, func(t *testing.T) {
				f := f.on(t)
				tx, err := f.q.Begin(f.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(f.ctx) //nolint:errcheck
				if err := ins(f, tx, a); err != nil {
					t.Fatalf("first row refused: %v", err)
				}
				err = ins(f, tx, b)
				if renewing(a.status, a.cancel) && renewing(b.status, b.cancel) {
					testkit.WantPgError(t, err, pgerrcode.UniqueViolation, "idx_one_renewing_subscription")
				} else if err != nil {
					t.Errorf("second row refused: %v", err)
				}
			})
		}
	}

	t.Run("ownership transfer", func(t *testing.T) {
		f := f.on(t)
		g := f.group()
		old := f.add("subscriptions", "group_id", g, "status", "active").id()
		testkit.WantPgError(t, f.insert("subscriptions", f.row("subscriptions", "group_id", g, "status", "trialing")),
			pgerrcode.UniqueViolation, "idx_one_renewing_subscription")
		f.exec(`UPDATE subscriptions SET cancel_at_period_end = TRUE WHERE id = $1`, old)
		f.add("subscriptions", "group_id", g, "status", "trialing")
		testkit.WantPgError(t, f.try(`UPDATE subscriptions SET cancel_at_period_end = FALSE WHERE id = $1`, old),
			pgerrcode.UniqueViolation, "idx_one_renewing_subscription")
		f.add("subscriptions", "status", "active") // another group
	})
}

// P0-SCHEMA-31
// A Stripe event id is recorded once. Recording it with ON CONFLICT DO
// NOTHING RETURNING tells a replay apart: the first delivery gets the row
// back, a replay gets no row, and of several deliveries arriving at once
// exactly one gets it.
func TestStripeEvents_ReplayReturnsNoRow(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	const record = `INSERT INTO stripe_events (event_id) VALUES ($1) ON CONFLICT (event_id) DO NOTHING RETURNING event_id`
	stored, now := f.inserted("stripe_events", f.row("stripe_events", "event_id", "evt_plain"))
	wantStored(t, stored, map[string]any{"processed_at": now})
	testkit.WantPgError(t, f.insert("stripe_events", row{"event_id": "evt_plain"}), pgerrcode.UniqueViolation, "stripe_events_pkey")

	var got string
	if err := f.q.QueryRow(f.ctx, record, "evt_1").Scan(&got); err != nil || got != "evt_1" {
		t.Fatalf("first delivery: %q, %v", got, err)
	}
	if err := f.q.QueryRow(f.ctx, record, "evt_1").Scan(&got); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("replay: got %v, want no row", err)
	}

	const n = 4
	firsts := make([]bool, n)
	errs := testkit.Race(t, f.q, n, func(ctx context.Context, i int, tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, record, "evt_2").Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		firsts[i] = err == nil
		return err
	})
	winners := 0
	for i, err := range errs {
		if err != nil {
			t.Errorf("delivery %d: %v", i, err)
		}
		if firsts[i] {
			winners++
		}
	}
	if winners != 1 {
		t.Errorf("%d concurrent deliveries got the row, want exactly 1", winners)
	}
}

// ---- email_outbox, email_suppressions, email_events ---------------------

// P0-SCHEMA-32
// An outbox row starts pending with no attempts, due now, with empty
// headers; stream is auth or notify; status is one of six; group, SES
// message id, last error and sent time may be empty; a dedupe key is
// enqueued once, so ON CONFLICT DO NOTHING turns a repeat into nothing.
func TestEmailOutbox_DefaultsChecksAndDedupe(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	stored, now := f.inserted("email_outbox", f.row("email_outbox"))
	wantStored(t, stored, map[string]any{
		"status":          "pending",
		"attempts":        float64(0),
		"headers":         map[string]any{},
		"next_attempt_at": now,
		"created_at":      now,
		"group_id":        nil,
		"ses_message_id":  nil,
		"last_error":      nil,
		"sent_at":         nil,
	})
	wantValues(t, f, "email_outbox", "stream", "email_outbox_stream_check", []any{"auth", "notify"}, []any{"", "mail", "Auth", "sms"})
	wantValues(t, f, "email_outbox", "status", "email_outbox_status_check",
		[]any{"pending", "sending", "sent", "failed", "dead", "suppressed"}, []any{"", "queued", "bounced", "Sent"})
	f.add("email_outbox", "group_id", f.group(), "ses_message_id", "ses-1", "last_error", "timeout", "sent_at", t0)

	f.add("email_outbox", "dedupe_key", "rsvp_confirm:m1:ann@example.com")
	testkit.WantPgError(t, f.insert("email_outbox", f.row("email_outbox", "dedupe_key", "rsvp_confirm:m1:ann@example.com")),
		pgerrcode.UniqueViolation, "email_outbox_dedupe_key_key")
	sql, args := insertSQL("email_outbox", f.row("email_outbox", "dedupe_key", "rsvp_confirm:m1:ann@example.com"), "ON CONFLICT (dedupe_key) DO NOTHING")
	if n := f.exec(sql, args...); n != 0 {
		t.Errorf("repeat enqueue inserted %d rows, want 0", n)
	}
}

// P0-SCHEMA-32
// Deleting a group keeps its outbox rows, with the group cleared.
func TestEmailOutbox_GroupDeletionClearsGroupID(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	g := f.group()
	id := f.add("email_outbox", "group_id", g).id()
	f.exec(`DELETE FROM groups WHERE id = $1`, g)
	var group *uuid.UUID
	f.scan(`SELECT group_id FROM email_outbox WHERE id = $1`, []any{id}, &group)
	if group != nil {
		t.Errorf("group_id is %v, want NULL", group)
	}
}

// P0-SCHEMA-33
// A suppression is one row per address with reason hard_bounce, complaint
// or manual. A delivery event is one of five types, may lack an outbox row
// or group, and survives deletion of its outbox row with the link cleared.
func TestEmailSuppressionsAndEvents(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	wantValues(t, f, "email_suppressions", "reason", "email_suppressions_reason_check",
		[]any{"hard_bounce", "complaint", "manual"}, []any{"", "soft_bounce", "bounce", "Complaint"})
	stored, now := f.inserted("email_suppressions", f.row("email_suppressions", "email", "ann@example.com"))
	wantStored(t, stored, map[string]any{"created_at": now})
	testkit.WantPgError(t, f.insert("email_suppressions", f.row("email_suppressions", "email", "ann@example.com", "reason", "complaint")),
		pgerrcode.UniqueViolation, "email_suppressions_pkey")

	wantValues(t, f, "email_events", "event_type", "email_events_event_type_check",
		[]any{"delivery", "bounce_hard", "bounce_soft", "complaint", "reject"}, []any{"", "open", "click", "bounce", "Delivery"})
	f.add("email_events", "outbox_id", nil, "group_id", nil)
	outbox := f.add("email_outbox").id()
	ev := f.add("email_events", "outbox_id", outbox, "group_id", f.group()).id()
	f.exec(`DELETE FROM email_outbox WHERE id = $1`, outbox)
	var link *uuid.UUID
	f.scan(`SELECT outbox_id FROM email_events WHERE id = $1`, []any{ev}, &link)
	if link != nil {
		t.Errorf("outbox_id is %v after the outbox row was deleted, want NULL", link)
	}
}
