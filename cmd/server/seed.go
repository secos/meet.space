package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"meet.space/internal/config"
	"meet.space/internal/database"
)

// Seed values for local development. The group is served on
// hikers.<PUBLIC_HOST> (hikers.localhost:8080 under docker compose), as
// development-plan.md "Testing custom domains locally" describes.
const (
	seedSlug       = "hikers"
	seedGroupName  = "Front Range Hikers"
	seedOwnerEmail = "owner@example.com"
	seedTimezone   = "America/Denver"
	paidStorage    = 25 << 30 // 25 GB (Phase 10 step 2)
)

// SeedDomain is the seeded group's custom domain for a public host.
func SeedDomain(publicHost string) string { return "hikers." + publicHost }

// seedDev creates (or resets) one paid group with an active custom domain
// and an owner. It is idempotent and refuses to run in production.
func seedDev(ctx context.Context, cfg config.Config, stdout io.Writer) error {
	if cfg.Env == config.EnvProduction {
		return errors.New("seed-dev refuses to run with ENV=production")
	}
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	domain := strings.ToLower(SeedDomain(cfg.PublicHost))
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var userID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, 'Dev Owner')
			ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email RETURNING id`,
			uuid.Must(uuid.NewV7()), seedOwnerEmail).Scan(&userID); err != nil {
			return fmt.Errorf("owner: %w", err)
		}
		var groupID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO groups (id, slug, name, description, topic, timezone,
				tier, member_limit, storage_limit_bytes, custom_domain, custom_domain_status)
			VALUES ($1, $2, $3, 'A local test group.', 'Hiking', $4, 'paid', NULL, $5, $6, 'active')
			ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name, tier = 'paid', member_limit = NULL,
				storage_limit_bytes = EXCLUDED.storage_limit_bytes,
				custom_domain = EXCLUDED.custom_domain, custom_domain_status = 'active'
			RETURNING id`,
			uuid.Must(uuid.NewV7()), seedSlug, seedGroupName, seedTimezone, int64(paidStorage), domain).Scan(&groupID); err != nil {
			return fmt.Errorf("group: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO memberships (id, user_id, group_id, role) VALUES ($1, $2, $3, 'owner')
			ON CONFLICT (user_id, group_id) DO NOTHING`, uuid.Must(uuid.NewV7()), userID, groupID); err != nil {
			return fmt.Errorf("owner membership: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO notification_preferences (id, user_id, group_id) VALUES ($1, $2, $3)
			ON CONFLICT (user_id, group_id) DO NOTHING`, uuid.Must(uuid.NewV7()), userID, groupID); err != nil {
			return fmt.Errorf("owner preferences: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "seeded group %q on %s\n", seedSlug, domain)
	return nil
}
