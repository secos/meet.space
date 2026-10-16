package apptest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"meet.space/internal/testkit"
)

// Role is who a request is sent as.
type Role string

const (
	Anonymous     Role = "anonymous"
	NonMember     Role = "user"           // logged in, not in the group
	Member        Role = "member"         // member of the group
	CoOrganizer   Role = "co_organizer"   // co-organizer of the group
	Owner         Role = "owner"          // owner of the group
	PlatformAdmin Role = "platform_admin" // users.is_platform_admin
)

// Roles is every role, for matrix tests.
var Roles = []Role{Anonymous, NonMember, Member, CoOrganizer, Owner, PlatformAdmin}

// SessionCookieName is the session cookie the helper sets. Phase 1's
// session layer must use the same name (a structural test will tie them).
const SessionCookieName = "session"

// Fixture is the shared seed: an active custom-domain group, a group whose
// domain is still pending DNS, a user in each role, and private data that
// must never appear in a cacheable page (Security Rule 7).
type Fixture struct {
	Hikers  uuid.UUID // active custom domain GroupHost, paid
	Pending uuid.UUID // custom domain PendingHost, pending_dns
	Users   map[Role]uuid.UUID
	Emails  map[Role]string

	// Private data, all distinctive so a substring check can't pass by
	// accident.
	RSVPEmail  string
	VirtualURL string
	Meeting    uuid.UUID
}

// HikersName is the seeded group's name.
const HikersName = "Front Range Hikers"

// Seed creates the shared fixture and refreshes the domain map.
func (a *App) Seed() *Fixture {
	a.T.Helper()
	ctx := context.Background()
	tag := testkit.RandHex(4)
	f := &Fixture{
		Hikers:     uuid.Must(uuid.NewV7()),
		Pending:    uuid.Must(uuid.NewV7()),
		Users:      map[Role]uuid.UUID{},
		Emails:     map[Role]string{},
		RSVPEmail:  "rule7-rsvp-" + tag + "@example.com",
		VirtualURL: "https://private.example.test/rule7-" + tag,
		Meeting:    uuid.Must(uuid.NewV7()),
	}
	err := pgx.BeginFunc(ctx, a.DB.Pool, func(tx pgx.Tx) error {
		groups := []struct {
			id           uuid.UUID
			slug, name   string
			domain, stat string
			tier         string
		}{
			{f.Hikers, "hikers", HikersName, GroupHost, "active", "paid"},
			{f.Pending, "pending", "Pending Group", PendingHost, "pending_dns", "paid"},
		}
		for _, g := range groups {
			if _, err := tx.Exec(ctx, `INSERT INTO groups (id, slug, name, timezone, tier, member_limit,
				storage_limit_bytes, custom_domain, custom_domain_status)
				VALUES ($1, $2, $3, 'America/Chicago', $4, NULL, 26843545600, $5, $6)`,
				g.id, g.slug, g.name, g.tier, g.domain, g.stat); err != nil {
				return err
			}
		}
		for _, r := range Roles[1:] {
			id := uuid.Must(uuid.NewV7())
			email := "rule7-" + string(r) + "-" + tag + "@example.com"
			f.Users[r], f.Emails[r] = id, email
			if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, name, is_platform_admin) VALUES ($1, $2, $3, $4)`,
				id, email, "Test "+string(r), r == PlatformAdmin); err != nil {
				return err
			}
			var role string
			switch r {
			case Member, CoOrganizer, Owner:
				role = string(r)
			default:
				continue
			}
			if _, err := tx.Exec(ctx, `INSERT INTO memberships (id, user_id, group_id, role) VALUES ($1, $2, $3, $4)`,
				uuid.Must(uuid.NewV7()), id, f.Hikers, role); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO meetings (id, group_id, title, starts_at, timezone, meeting_type, virtual_url)
			VALUES ($1, $2, 'Rule 7 private meeting', $3, 'America/Chicago', 'virtual', $4)`,
			f.Meeting, f.Hikers, a.Clock.Now().Add(72*time.Hour), f.VirtualURL); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO rsvps (id, meeting_id, email, name) VALUES ($1, $2, $3, 'Rule Seven')`,
			uuid.Must(uuid.NewV7()), f.Meeting, f.RSVPEmail)
		return err
	})
	if err != nil {
		a.T.Fatalf("seed fixture: %v", err)
	}
	a.RefreshDomains()
	return f
}

// SeedSession inserts a session for user bound to host, by the schema's
// contract (sessions.host is the host; token_hash is the SHA-256 of the
// cookie value), and returns the cookie value.
func (a *App) SeedSession(user uuid.UUID, host string) string {
	a.T.Helper()
	token := testkit.RandHex(32)
	sum := sha256.Sum256([]byte(token))
	if _, err := a.DB.Pool.Exec(context.Background(), `INSERT INTO sessions (id, user_id, host, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, uuid.Must(uuid.NewV7()), user, host, hex.EncodeToString(sum[:]),
		a.Clock.Now().Add(30*24*time.Hour)); err != nil {
		a.T.Fatalf("seed session: %v", err)
	}
	return token
}

// As makes the client act as role on host: for a logged-in role it seeds a
// session for the fixture's user of that role, bound to host, and puts the
// cookie in host's jar. Anonymous clears nothing and adds nothing.
func (c *Client) As(a *App, f *Fixture, role Role, host string) *Client {
	c.t.Helper()
	if role == Anonymous {
		return c
	}
	token := a.SeedSession(f.Users[role], host)
	c.jar(host).SetCookies(&url.URL{Scheme: "https", Host: host, Path: "/"}, []*http.Cookie{{
		Name: SessionCookieName, Value: token, Path: "/", Secure: true, HttpOnly: true,
	}})
	return c
}
