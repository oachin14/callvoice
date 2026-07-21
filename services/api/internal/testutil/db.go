package testutil

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const defaultDatabaseURL = "postgres://callvoice:callvoice@localhost:5432/callvoice?sslmode=disable"

func DatabaseURL() string {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultDatabaseURL
	}
	return databaseURL
}

func OpenTestDB(t *testing.T) *sql.DB {
	t.Helper()

	databaseURL := DatabaseURL()

	conn, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}

	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		t.Skipf("postgres unavailable: %v", err)
	}

	return conn
}
