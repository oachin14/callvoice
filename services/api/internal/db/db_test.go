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

	upFiles := make(map[string]bool)
	downFiles := make(map[string]bool)
	for _, entry := range entries {
		switch {
		case strings.HasSuffix(entry.Name(), ".up.sql"):
			upFiles[entry.Name()] = true
		case strings.HasSuffix(entry.Name(), ".down.sql"):
			downFiles[entry.Name()] = true
		}
	}

	require.NotEmpty(t, upFiles, "expected at least one .up.sql migration")
	require.NotEmpty(t, downFiles, "expected at least one .down.sql migration")

	initUp, err := apimigrations.Files.ReadFile("0001_init.up.sql")
	require.NoError(t, err)
	require.Contains(t, string(initUp), "CREATE TABLE users")
	require.Contains(t, string(initUp), "CREATE TABLE carriers")
	require.Contains(t, string(initUp), "CREATE TABLE dids")

	campaignsUp, err := apimigrations.Files.ReadFile("0002_campaigns_ops.up.sql")
	require.NoError(t, err)
	require.Contains(t, string(campaignsUp), "CREATE TABLE campaigns")
	require.Contains(t, string(campaignsUp), "CREATE TABLE call_logs")

	initDown, err := apimigrations.Files.ReadFile("0001_init.down.sql")
	require.NoError(t, err)
	require.Contains(t, string(initDown), "DROP TABLE")

	campaignsDown, err := apimigrations.Files.ReadFile("0002_campaigns_ops.down.sql")
	require.NoError(t, err)
	require.Contains(t, string(campaignsDown), "DROP TABLE")
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

func TestMigrateAndInsertCampaign(t *testing.T) {
	conn := testutil.OpenTestDB(t)
	defer conn.Close()

	databaseURL := testutil.DatabaseURL()
	require.NoError(t, migrate.Up(databaseURL))
	require.NoError(t, conn.Ping())

	var carrierID string
	err := conn.QueryRow(
		`INSERT INTO carriers (name, host) VALUES ($1, $2) RETURNING id`,
		fmt.Sprintf("carrier-%s", t.Name()),
		"sip.example.com",
	).Scan(&carrierID)
	require.NoError(t, err)

	var campaignID string
	err = conn.QueryRow(
		`INSERT INTO campaigns (name, carrier_id, status, dial_mode)
		 VALUES ($1, $2, 'draft', 'manual')
		 RETURNING id`,
		fmt.Sprintf("campaign-%s", t.Name()),
		carrierID,
	).Scan(&campaignID)
	require.NoError(t, err)
	require.NotEmpty(t, campaignID)
}
