package migrate

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"

	apimigrations "github.com/callvoice/callvoice/services/api/migrations"
)

func Up(databaseURL string) error {
	return run(databaseURL, func(m *migrate.Migrate) error {
		return m.Up()
	}, "apply migrations")
}

func Down(databaseURL string) error {
	return run(databaseURL, func(m *migrate.Migrate) error {
		return m.Down()
	}, "rollback migrations")
}

func run(databaseURL string, step func(*migrate.Migrate) error, action string) error {
	if databaseURL == "" {
		return fmt.Errorf("database URL is empty")
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open database for migration: %w", err)
	}
	defer db.Close()

	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("create postgres migrate driver: %w", err)
	}

	source, err := iofs.New(apimigrations.Files, ".")
	if err != nil {
		return fmt.Errorf("load embedded migrations: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer m.Close()

	if err := step(m); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("%s: %w", action, err)
	}

	return nil
}
