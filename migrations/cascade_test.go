package migrations_test

import (
	"testing"

	"github.com/google/uuid"
)

// wantCount compares a count query with what should be left.
func wantCount(t *testing.T, f *fx, what string, want int, sql string, args ...any) {
	t.Helper()
	if got := f.count(sql, args...); got != want {
		t.Errorf("%s: %d rows, want %d", what, got, want)
	}
}

// P0-SCHEMA-34
// Deleting a user (workflows.md §14: "cascades memberships, sessions,
// posts, preferences; RSVPs keep email, user_id → NULL") removes their
// sessions on every host, memberships, preferences and posts, and with
// their feed posts every reply to them (other people's included) and every
// attachment on those posts. Their RSVPs and the subscriptions they paid
// for stay, with the user link cleared. Rows keyed by email address (magic
// links, opt-outs, suppressions, outbox) are not touched by the database,
// and nobody else's rows change.
// OPEN: this is only what the database does on DELETE FROM users. Whether
// account deletion should also erase the address from RSVPs or the
// email-keyed rows is decisions.md question 5 (erasure policy), still open;
// nothing here asserts an app-level policy.
func TestUserDeletion_Cascades(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	ann := f.user("email", "ann@example.com")
	bob := f.user("email", "bob@example.com")
	owner := f.user()
	g1, g2 := f.group(), f.group()
	f.member(owner, g1, "owner")
	f.member(owner, g2, "owner")
	f.member(ann, g1, "member")
	f.member(ann, g2, "co_organizer")
	f.member(bob, g1, "member")

	f.add("sessions", "user_id", ann, "host", "localhost:8080")
	f.add("sessions", "user_id", ann, "host", "hikers.localhost:8080")
	f.add("sessions", "user_id", bob)
	f.add("notification_preferences", "user_id", ann, "group_id", g1)
	f.add("notification_preferences", "user_id", ann, "group_id", g2)
	f.add("notification_preferences", "user_id", bob, "group_id", g1)

	annFeed := f.post(g1, "user_id", ann)
	bobReplyToAnn := f.post(g1, "user_id", bob, "parent_id", annFeed)
	f.add("attachments", "post_id", annFeed, "group_id", g1)
	f.add("attachments", "post_id", bobReplyToAnn, "group_id", g1)
	bobFeed := f.post(g1, "user_id", bob)
	annReplyToBob := f.post(g1, "user_id", ann, "parent_id", bobFeed)
	m := f.meeting(g1)
	annComment := f.post(g1, "user_id", ann, "meeting_id", m)
	bobComment := f.post(g1, "user_id", bob, "meeting_id", m)
	bobAttachment := f.add("attachments", "post_id", bobFeed, "group_id", g1).id()

	annRSVP := f.add("rsvps", "meeting_id", m, "email", "ann@example.com", "user_id", ann).id()
	f.add("rsvps", "meeting_id", m, "email", "bob@example.com", "user_id", bob)
	sub := f.add("subscriptions", "group_id", g1, "payer_user_id", ann).id()

	f.add("magic_links", "email", "ann@example.com")
	f.add("email_opt_outs", "email", "ann@example.com", "group_id", g1)
	f.add("email_suppressions", "email", "ann@example.com")
	f.add("email_outbox", "to_email", "ann@example.com", "group_id", g1)

	if n := f.exec(`DELETE FROM users WHERE id = $1`, ann); n != 1 {
		t.Fatalf("deleted %d users", n)
	}

	wantCount(t, f, "ann's sessions on every host", 0, `SELECT count(*) FROM sessions WHERE user_id = $1`, ann)
	wantCount(t, f, "ann's memberships", 0, `SELECT count(*) FROM memberships WHERE user_id = $1`, ann)
	wantCount(t, f, "ann's preferences", 0, `SELECT count(*) FROM notification_preferences WHERE user_id = $1`, ann)
	wantCount(t, f, "ann's posts", 0, `SELECT count(*) FROM posts WHERE user_id = $1`, ann)
	for _, gone := range []uuid.UUID{annFeed, bobReplyToAnn, annReplyToBob, annComment} {
		wantCount(t, f, "post "+gone.String(), 0, `SELECT count(*) FROM posts WHERE id = $1`, gone)
	}
	wantCount(t, f, "attachments on removed posts", 0, `SELECT count(*) FROM attachments WHERE post_id = ANY ($1)`, []uuid.UUID{annFeed, bobReplyToAnn})

	var email string
	var rsvpUser, payer *uuid.UUID
	f.scan(`SELECT email, user_id FROM rsvps WHERE id = $1`, []any{annRSVP}, &email, &rsvpUser)
	if email != "ann@example.com" || rsvpUser != nil {
		t.Errorf("ann's RSVP: email %q user_id %v, want her email kept and NULL", email, rsvpUser)
	}
	f.scan(`SELECT payer_user_id FROM subscriptions WHERE id = $1`, []any{sub}, &payer)
	if payer != nil {
		t.Errorf("subscription payer is %v, want NULL", payer)
	}

	for table, col := range map[string]string{
		"magic_links": "email", "email_opt_outs": "email", "email_suppressions": "email", "email_outbox": "to_email",
	} {
		wantCount(t, f, table+" for ann's address", 1, `SELECT count(*) FROM `+table+` WHERE `+col+` = 'ann@example.com'`)
	}

	wantCount(t, f, "other users", 2, `SELECT count(*) FROM users WHERE id IN ($1, $2)`, bob, owner)
	wantCount(t, f, "bob's session", 1, `SELECT count(*) FROM sessions WHERE user_id = $1`, bob)
	wantCount(t, f, "other memberships", 3, `SELECT count(*) FROM memberships WHERE user_id IN ($1, $2)`, bob, owner)
	wantCount(t, f, "bob's preferences", 1, `SELECT count(*) FROM notification_preferences WHERE user_id = $1`, bob)
	wantCount(t, f, "bob's own feed post and comment", 2, `SELECT count(*) FROM posts WHERE id IN ($1, $2)`, bobFeed, bobComment)
	wantCount(t, f, "bob's attachment", 1, `SELECT count(*) FROM attachments WHERE id = $1`, bobAttachment)
	wantCount(t, f, "bob's RSVP", 1, `SELECT count(*) FROM rsvps WHERE user_id = $1`, bob)
}

// groupRows counts every row that belongs to group g, table by table.
func groupRows(f *fx, g uuid.UUID) map[string]int {
	f.t.Helper()
	return map[string]int{
		"memberships":              f.count(`SELECT count(*) FROM memberships WHERE group_id = $1`, g),
		"meetings":                 f.count(`SELECT count(*) FROM meetings WHERE group_id = $1`, g),
		"rsvps":                    f.count(`SELECT count(*) FROM rsvps r JOIN meetings m ON m.id = r.meeting_id WHERE m.group_id = $1`, g),
		"posts":                    f.count(`SELECT count(*) FROM posts WHERE group_id = $1`, g),
		"attachments":              f.count(`SELECT count(*) FROM attachments WHERE group_id = $1`, g),
		"notification_preferences": f.count(`SELECT count(*) FROM notification_preferences WHERE group_id = $1`, g),
		"email_opt_outs":           f.count(`SELECT count(*) FROM email_opt_outs WHERE group_id = $1`, g),
		"subscriptions":            f.count(`SELECT count(*) FROM subscriptions WHERE group_id = $1`, g),
		"email_outbox":             f.count(`SELECT count(*) FROM email_outbox WHERE group_id = $1`, g),
		"email_events":             f.count(`SELECT count(*) FROM email_events WHERE group_id = $1`, g),
	}
}

// seedGroup gives group g a row in every table that references groups.
func seedGroup(f *fx, g uuid.UUID) (outbox uuid.UUID) {
	f.t.Helper()
	owner, member := f.user(), f.user()
	f.member(owner, g, "owner")
	f.member(member, g, "member")
	m := f.meeting(g)
	f.add("rsvps", "meeting_id", m, "user_id", member)
	feed := f.post(g, "user_id", owner)
	f.post(g, "user_id", member, "parent_id", feed)
	f.post(g, "user_id", member, "meeting_id", m)
	f.add("attachments", "post_id", feed, "group_id", g)
	f.add("notification_preferences", "user_id", member, "group_id", g)
	f.add("email_opt_outs", "group_id", g)
	f.add("subscriptions", "group_id", g, "payer_user_id", owner)
	outbox = f.add("email_outbox", "group_id", g).id()
	f.add("email_events", "outbox_id", outbox, "group_id", g)
	f.add("magic_links", "purpose", "join_group", "metadata", map[string]any{"group_id": g.String()})
	return outbox
}

// P0-SCHEMA-35
// Deleting a group removes everything that belongs to it (memberships,
// meetings and their RSVPs, posts and attachments, preferences, opt-outs,
// subscriptions, delivery events). Its outbox rows stay, with the group
// cleared. Users, magic links (whose group is only inside metadata) and
// every other group's rows are untouched.
func TestGroupDeletion_Cascades(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	g, other := f.group(), f.group()
	outbox := seedGroup(f, g)
	seedGroup(f, other)
	users := f.rowCount("users")
	links := f.rowCount("magic_links")
	otherBefore := groupRows(f, other)
	before := groupRows(f, g)
	totalBefore := map[string]int{}
	for table, n := range before {
		if n == 0 {
			t.Fatalf("seed left no %s for the group", table)
		}
		totalBefore[table] = f.rowCount(table)
	}

	f.exec(`DELETE FROM groups WHERE id = $1`, g)

	for table, n := range groupRows(f, g) {
		if n != 0 {
			t.Errorf("%d %s rows still reference the deleted group", n, table)
		}
	}
	// Counting by group_id alone can't tell a deleted row from one whose
	// group_id was set to NULL, so the totals must also drop by the group's
	// rows: removed, not detached. The outbox is the one table whose rows
	// stay.
	for table, total := range totalBefore {
		want := total - before[table]
		if table == "email_outbox" {
			want = total
		}
		if got := f.rowCount(table); got != want {
			t.Errorf("%s holds %d rows after the group's deletion, want %d", table, got, want)
		}
	}
	var group *uuid.UUID
	f.scan(`SELECT group_id FROM email_outbox WHERE id = $1`, []any{outbox}, &group)
	if group != nil {
		t.Errorf("outbox row's group_id is %v, want NULL", group)
	}
	wantCount(t, f, "users", users, `SELECT count(*) FROM users`)
	wantCount(t, f, "magic links", links, `SELECT count(*) FROM magic_links`)
	for table, n := range groupRows(f, other) {
		if n != otherBefore[table] {
			t.Errorf("other group's %s went from %d to %d rows", table, otherBefore[table], n)
		}
	}
}

// P0-SCHEMA-35
// Deleting a meeting removes its RSVPs and comments, and nothing of the
// group's other meetings or feed.
func TestMeetingDeletion_RemovesItsRSVPsAndComments(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	g := f.group()
	m, kept := f.meeting(g), f.meeting(g)
	var comments []uuid.UUID
	for _, mt := range []uuid.UUID{m, kept} {
		f.add("rsvps", "meeting_id", mt)
		f.add("rsvps", "meeting_id", mt)
		comments = append(comments, f.post(g, "meeting_id", mt))
	}
	feed := f.post(g)

	f.exec(`DELETE FROM meetings WHERE id = $1`, m)

	// Comments are looked up by id: posts.meeting_id is nullable, so a
	// count by meeting_id would also be 0 if the comment had been kept and
	// detached (turning it into a feed post).
	wantCount(t, f, "RSVPs of the deleted meeting", 0, `SELECT count(*) FROM rsvps WHERE meeting_id = $1`, m)
	wantCount(t, f, "comment on the deleted meeting", 0, `SELECT count(*) FROM posts WHERE id = $1`, comments[0])
	wantCount(t, f, "RSVPs of the other meeting", 2, `SELECT count(*) FROM rsvps WHERE meeting_id = $1`, kept)
	wantCount(t, f, "comment on the other meeting", 1, `SELECT count(*) FROM posts WHERE id = $1 AND meeting_id = $2`, comments[1], kept)
	wantCount(t, f, "feed post", 1, `SELECT count(*) FROM posts WHERE id = $1`, feed)
	wantCount(t, f, "posts left", 2, `SELECT count(*) FROM posts`)
}
