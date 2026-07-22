package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/callvoice/callvoice/internal/authkit"
	"github.com/callvoice/callvoice/internal/cryptokit"
	"github.com/callvoice/callvoice/internal/models"
	"github.com/callvoice/callvoice/services/api/internal/db"
	"github.com/callvoice/callvoice/services/api/internal/store"
)

const adminEmail = "admin@callvoice.local"

const (
	demoAgentEmail   = "agent@callvoice.local"
	demoCampaignName = "Demo Campaign"
	demoCarrierName  = "lab-carrier"
)

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
	if err := seedDemo(ctx, conn, password, carrierKey); err != nil {
		log.Fatalf("seed demo: %v", err)
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

func seedDemo(ctx context.Context, conn *sql.DB, adminPassword string, carrierKey []byte) error {
	if os.Getenv("SEED_DEMO") != "1" {
		return nil
	}

	agentPassword := os.Getenv("SEED_DEMO_AGENT_PASSWORD")
	if agentPassword == "" {
		agentPassword = adminPassword
	}
	if agentPassword == "" {
		return errors.New("SEED_DEMO=1 requires SEED_DEMO_AGENT_PASSWORD or SEED_ADMIN_PASSWORD")
	}

	agentID, err := upsertDemoAgent(ctx, conn, agentPassword)
	if err != nil {
		return err
	}

	carrierID, err := ensureLabCarrier(ctx, conn, carrierKey)
	if err != nil {
		return err
	}

	campaignStore := &store.CampaignStore{DB: conn}
	leadStore := &store.LeadStore{DB: conn}

	campaignID, err := findCampaignByName(ctx, conn, demoCampaignName)
	if errors.Is(err, sql.ErrNoRows) {
		created, err := campaignStore.Create(ctx, store.CreateCampaignInput{
			Name:      demoCampaignName,
			CarrierID: carrierID,
		})
		if err != nil {
			return fmt.Errorf("create demo campaign: %w", err)
		}
		campaignID = created.ID
		fmt.Printf("Created demo campaign %s\n", campaignID)
	} else if err != nil {
		return fmt.Errorf("lookup demo campaign: %w", err)
	} else {
		fmt.Printf("Demo campaign %s already exists\n", campaignID)
	}

	if err := campaignStore.SetAgents(ctx, campaignID, []uuid.UUID{agentID}); err != nil {
		return fmt.Errorf("assign demo agent: %w", err)
	}

	running := models.CampaignStatusRunning
	if _, err := campaignStore.Update(ctx, campaignID, store.UpdateCampaignInput{Status: &running}); err != nil {
		return fmt.Errorf("start demo campaign: %w", err)
	}

	leadCount, err := countCampaignLeads(ctx, conn, campaignID)
	if err != nil {
		return err
	}
	if leadCount == 0 {
		result, err := leadStore.Import(ctx, store.ImportLeadsInput{
			CampaignID: campaignID,
			Name:       "demo-import",
			Rows: []store.ImportLeadRow{
				{Phone: "+33111111101", Payload: map[string]string{"name": "Alice Demo"}},
				{Phone: "+33111111102", Payload: map[string]string{"name": "Bob Demo"}},
				{Phone: "+33111111103", Payload: map[string]string{"name": "Carol Demo"}},
			},
		})
		if err != nil {
			return fmt.Errorf("import demo leads: %w", err)
		}
		fmt.Printf("Imported %d demo lead(s) into list %s\n", result.Imported, result.ListID)
	} else {
		fmt.Printf("Demo campaign already has %d lead(s); skipping import\n", leadCount)
	}

	fmt.Println("=== Demo lab credentials ===")
	fmt.Printf("Agent email: %s\n", demoAgentEmail)
	fmt.Printf("Agent password: (from SEED_DEMO_AGENT_PASSWORD or SEED_ADMIN_PASSWORD)\n")
	fmt.Printf("Campaign: %s (%s)\n", demoCampaignName, campaignID)
	return nil
}

func upsertDemoAgent(ctx context.Context, conn *sql.DB, password string) (uuid.UUID, error) {
	hash, err := authkit.HashPassword(password)
	if err != nil {
		return uuid.Nil, fmt.Errorf("hash agent password: %w", err)
	}

	displayName := "Demo Agent"
	userStore := &store.UserStore{DB: conn}

	var userID uuid.UUID
	err = conn.QueryRowContext(ctx,
		`SELECT id FROM users WHERE email = lower($1)`,
		demoAgentEmail,
	).Scan(&userID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		created, err := userStore.Create(ctx, store.CreateUserInput{
			Email:        demoAgentEmail,
			PasswordHash: hash,
			Role:         models.UserRoleAgent,
			DisplayName:  &displayName,
		})
		if err != nil {
			return uuid.Nil, fmt.Errorf("create demo agent: %w", err)
		}
		fmt.Printf("Created demo agent %s\n", created.ID)
		return created.ID, nil
	case err != nil:
		return uuid.Nil, fmt.Errorf("lookup demo agent: %w", err)
	}

	_, err = conn.ExecContext(ctx, `
		UPDATE users
		SET password_hash = $2,
		    display_name = $3,
		    failed_login_count = 0,
		    locked_until = NULL,
		    disabled_at = NULL
		WHERE id = $1
	`, userID, hash, displayName)
	if err != nil {
		return uuid.Nil, fmt.Errorf("update demo agent: %w", err)
	}
	fmt.Printf("Demo agent %s already exists (password refreshed)\n", userID)
	return userID, nil
}

func ensureLabCarrier(ctx context.Context, conn *sql.DB, carrierKey []byte) (uuid.UUID, error) {
	var carrierID uuid.UUID
	err := conn.QueryRowContext(ctx, `
		SELECT id FROM carriers WHERE name = $1
	`, demoCarrierName).Scan(&carrierID)
	if err == nil {
		return carrierID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("lookup lab carrier: %w", err)
	}

	err = conn.QueryRowContext(ctx, `SELECT id FROM carriers ORDER BY created_at ASC LIMIT 1`).Scan(&carrierID)
	if err == nil {
		fmt.Printf("Reusing existing carrier %s for demo campaign\n", carrierID)
		return carrierID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("lookup any carrier: %w", err)
	}

	password := []byte("lab-carrier-secret")
	encrypted, err := cryptokit.Encrypt(carrierKey, password)
	if err != nil {
		return uuid.Nil, fmt.Errorf("encrypt carrier password: %w", err)
	}
	username := "lab"
	carrierStore := &store.CarrierStore{DB: conn}
	created, err := carrierStore.Create(ctx, store.CreateCarrierInput{
		Name:              demoCarrierName,
		Host:              "sip.lab.test",
		Port:              5060,
		Transport:         "udp",
		Username:          &username,
		PasswordEncrypted: encrypted,
		Codecs:            []string{"PCMU", "PCMA"},
		MaxCPS:            30,
		MaxChannels:       100,
		Enabled:           true,
		Priority:          100,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("create lab carrier: %w", err)
	}
	fmt.Printf("Created lab carrier %s\n", created.ID)
	return created.ID, nil
}

func findCampaignByName(ctx context.Context, conn *sql.DB, name string) (uuid.UUID, error) {
	var id uuid.UUID
	err := conn.QueryRowContext(ctx, `SELECT id FROM campaigns WHERE name = $1`, name).Scan(&id)
	return id, err
}

func countCampaignLeads(ctx context.Context, conn *sql.DB, campaignID uuid.UUID) (int, error) {
	var count int
	err := conn.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM leads l
		JOIN lead_lists ll ON ll.id = l.list_id
		WHERE ll.campaign_id = $1
	`, campaignID).Scan(&count)
	return count, err
}
