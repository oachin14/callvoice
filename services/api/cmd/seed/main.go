package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/callvoice/callvoice/internal/authkit"
	"github.com/callvoice/callvoice/internal/cryptokit"
	"github.com/callvoice/callvoice/services/api/internal/db"
)

const adminEmail = "admin@callvoice.local"

var labDIDs = []struct {
	number      string
	destination string
}{
	{"+33123456789", "agent_pool:default"},
	{"+33987654321", "agent_pool:default"},
}

func main() {
	password := os.Getenv("SEED_ADMIN_PASSWORD")
	if password == "" {
		log.Fatal("SEED_ADMIN_PASSWORD is required")
	}

	carrierKeyRaw := os.Getenv("CARRIER_SECRET_KEY")
	if carrierKeyRaw == "" {
		carrierKeyRaw = "0123456789abcdef0123456789abcdef"
	}
	carrierKey, err := cryptokit.ParseKey(carrierKeyRaw)
	if err != nil {
		log.Fatalf("CARRIER_SECRET_KEY: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := db.OpenAndMigrate(ctx)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer conn.Close()

	if err := seedAdmin(ctx, conn, password, carrierKey); err != nil {
		log.Fatalf("seed admin: %v", err)
	}
	if err := seedDIDs(ctx, conn); err != nil {
		log.Fatalf("seed dids: %v", err)
	}

	log.Println("seed complete")
}

func seedAdmin(ctx context.Context, conn *sql.DB, password string, carrierKey []byte) error {
	hash, err := authkit.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	var userID string
	err = conn.QueryRowContext(ctx,
		`SELECT id::text FROM users WHERE email = lower($1)`,
		adminEmail,
	).Scan(&userID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		secret, err := authkit.GenerateTOTPSecret()
		if err != nil {
			return fmt.Errorf("generate totp secret: %w", err)
		}
		encrypted, err := cryptokit.Encrypt(carrierKey, []byte(secret))
		if err != nil {
			return fmt.Errorf("encrypt totp secret: %w", err)
		}

		_, err = conn.ExecContext(ctx, `
			INSERT INTO users (email, password_hash, role, totp_secret_encrypted, totp_enabled)
			VALUES (lower($1), $2, 'admin', $3, TRUE)
		`, adminEmail, hash, encrypted)
		if err != nil {
			return fmt.Errorf("insert admin: %w", err)
		}

		fmt.Println("=== Lab admin TOTP (save once) ===")
		fmt.Printf("Email: %s\n", adminEmail)
		fmt.Printf("TOTP secret: %s\n", secret)
		fmt.Printf("OTPAuth URL: %s\n", authkit.OTPAuthURL(secret, adminEmail))
		return nil
	case err != nil:
		return fmt.Errorf("lookup admin: %w", err)
	}

	_, err = conn.ExecContext(ctx, `
		UPDATE users
		SET password_hash = $2,
		    failed_login_count = 0,
		    locked_until = NULL
		WHERE id = $1::uuid
	`, userID, hash)
	if err != nil {
		return fmt.Errorf("update admin: %w", err)
	}

	fmt.Printf("Admin %s already exists (password refreshed, TOTP unchanged)\n", adminEmail)
	return nil
}

func seedDIDs(ctx context.Context, conn *sql.DB) error {
	for _, did := range labDIDs {
		_, err := conn.ExecContext(ctx, `
			INSERT INTO dids (number, destination)
			VALUES ($1, $2)
			ON CONFLICT (number) DO UPDATE SET destination = EXCLUDED.destination
		`, did.number, did.destination)
		if err != nil {
			return fmt.Errorf("upsert did %s: %w", did.number, err)
		}
	}
	fmt.Printf("Seeded %d lab DID stub(s)\n", len(labDIDs))
	return nil
}
