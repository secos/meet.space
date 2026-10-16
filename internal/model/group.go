// Package model holds the database types and queries.
package model

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Querier is the part of a pool or transaction the queries need.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// GroupSite is what a group's pages need to brand themselves.
type GroupSite struct {
	ID   uuid.UUID
	Name string
	Look string
	// PrimaryColor is the stored brand color, unvalidated; empty when the
	// group has none. Callers validate it before use (Security Rule 10).
	PrimaryColor string
}

// LoadGroupSite reads a group's name, look and brand color.
func LoadGroupSite(ctx context.Context, q Querier, id uuid.UUID) (GroupSite, error) {
	g := GroupSite{ID: id}
	err := q.QueryRow(ctx, `SELECT name, look, COALESCE(branding->>'primary_color', '')
		FROM groups WHERE id = $1`, id).Scan(&g.Name, &g.Look, &g.PrimaryColor)
	return g, err
}
