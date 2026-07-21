package db_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	apimigrations "github.com/callvoice/callvoice/services/api/migrations"
	"github.com/callvoice/callvoice/services/api/internal/db/migrate"
	"github.com/callvoice/callvoice/services/api/internal/testutil"
)

func TestMigrationFilesExistAndAreValidSQL(t *testing.T) {
	entries, err := apimigrations.Files.ReadDir(".")
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	var upFile, downFile string
	for _, entry := range entries {
		switch {
		case strings.HasSuffix(entry.Name(), ".up.sql"):
			upFile = entry.Name()
		case strings.HasSuffix(entry.Name(), ".down.sql"):
			downFile = entry.Name()
		}
	}

	require.NotEmpty(t, upFile, "expected at least one .up.sql migration")
	require.NotEmpty(t, downFile, "expected at least one .down.sql migration")

	upSQL, err := apimigrations.Files.ReadFile(upFile)
	require.NoError(t, err)
	require.Contains(t, string(upSQL), "CREATE TABLE users")
	require.Contains(t, string(upSQL), "CREATE TABLE carriers")
	require.Contains(t, string(upSQL), "CREATE TABLE dids")

	downSQL, err := apimigrations.Files.ReadFile(downFile)
	require.NoError(t, err)
	require.Contains(t, string(downSQL), "DROP TABLE")
}

func TestMigrateUpRequiresDatabaseURL(t *testing.T) {
	err := migrate.Up("")
	require.Error(t, err)
}

func TestMigrateAndInsertUser(t *testing.T) {
	conn := testutil.OpenTestDB(t)
	defer conn.Close()

	databaseURL := testutil.DatabaseURL()
	require.NoError(t, migrate.Down(databaseURL))
	require.NoError(t, migrate.Up(databaseURL))
	require.NoError(t, conn.Ping())

	email := fmt.Sprintf("admin-%s@test.local", t.Name())
	_, err := conn.Exec(
		`INSERT INTO users (email, password_hash, role) VALUES ($1, $2, 'admin')`,
		email,
		"x",
	)
	require.NoError(t, err)
}
