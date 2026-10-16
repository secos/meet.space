// Package migrate applies the embedded SQL migrations with golang-migrate.
// `server migrate` and the test harness both go through it, so tests run
// against exactly the schema production gets. Migrations never run on
// server startup.
package migrate

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"regexp"
	"strconv"

	gomigrate "github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers pgx5://
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"meet.space/migrations"
)

var versionRE = regexp.MustCompile(`^(\d+)_.*\.up\.sql$`)

// Latest is the highest migration version in the embedded files: the
// version the server requires before it will start.
func Latest() (uint, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return 0, err
	}
	var latest uint
	for _, e := range entries {
		m := versionRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		v, err := strconv.ParseUint(m[1], 10, 64)
		if err != nil {
			return 0, err
		}
		latest = max(latest, uint(v))
	}
	if latest == 0 {
		return 0, errors.New("no migrations embedded")
	}
	return latest, nil
}

func open(databaseURL string) (*gomigrate.Migrate, error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("database url: %w", err)
	}
	u.Scheme = "pgx5"
	return gomigrate.NewWithSourceInstance("iofs", src, u.String())
}

func closeAll(m *gomigrate.Migrate, err error) error {
	srcErr, dbErr := m.Close()
	return errors.Join(err, srcErr, dbErr)
}

// Up applies every pending migration.
func Up(databaseURL string) error {
	m, err := open(databaseURL)
	if err != nil {
		return err
	}
	err = m.Up()
	if errors.Is(err, gomigrate.ErrNoChange) {
		err = nil
	}
	return closeAll(m, err)
}

// Down reverts the most recent migration only.
func Down(databaseURL string) error {
	m, err := open(databaseURL)
	if err != nil {
		return err
	}
	return closeAll(m, m.Steps(-1))
}

// Version reports the database's migration version. A database that was
// never migrated reports 0.
func Version(databaseURL string) (version uint, dirty bool, err error) {
	m, err := open(databaseURL)
	if err != nil {
		return 0, false, err
	}
	version, dirty, err = m.Version()
	if errors.Is(err, gomigrate.ErrNilVersion) {
		version, dirty, err = 0, false, nil
	}
	return version, dirty, closeAll(m, err)
}
