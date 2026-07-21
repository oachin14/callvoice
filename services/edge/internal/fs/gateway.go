package fs

import (
	"context"
	"database/sql"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/callvoice/callvoice/internal/cryptokit"
	"github.com/callvoice/callvoice/internal/models"
)

const externalProfile = "external"

// ApplyCarrierGateway writes gateway XML and asks Sofia to reload it.
func ApplyCarrierGateway(esl APIClient, gatewayDir string, c models.Carrier, password string) error {
	if err := os.MkdirAll(gatewayDir, 0o755); err != nil {
		return fmt.Errorf("gateway dir: %w", err)
	}
	name := GatewayName(c.ID)
	body, err := renderGatewayXML(c, password)
	if err != nil {
		return err
	}
	path := filepath.Join(gatewayDir, c.ID.String()+".xml")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("write gateway xml: %w", err)
	}

	// Best-effort unload before rescan so credentials/proxy changes take effect.
	_, _ = esl.API(fmt.Sprintf("sofia profile %s killgw %s", externalProfile, name))
	if _, err := esl.API(fmt.Sprintf("sofia profile %s rescan", externalProfile)); err != nil {
		return fmt.Errorf("sofia rescan: %w", err)
	}
	return nil
}

// RemoveCarrierGateway deletes gateway XML and unloads it from Sofia.
func RemoveCarrierGateway(esl APIClient, gatewayDir string, id uuid.UUID) error {
	path := filepath.Join(gatewayDir, id.String()+".xml")
	_ = os.Remove(path)
	name := GatewayName(id)
	if _, err := esl.API(fmt.Sprintf("sofia profile %s killgw %s", externalProfile, name)); err != nil {
		// Missing gateway is fine (already gone / never loaded).
		if !strings.Contains(strings.ToLower(err.Error()), "invalid") &&
			!strings.Contains(strings.ToLower(err.Error()), "not find") {
			return fmt.Errorf("sofia killgw: %w", err)
		}
	}
	return nil
}

// GatewayName is the Sofia gateway name for a carrier id.
func GatewayName(id uuid.UUID) string {
	return id.String()
}

func renderGatewayXML(c models.Carrier, password string) ([]byte, error) {
	type param struct {
		XMLName xml.Name `xml:"param"`
		Name    string   `xml:"name,attr"`
		Value   string   `xml:"value,attr"`
	}
	type gateway struct {
		XMLName xml.Name `xml:"gateway"`
		Name    string   `xml:"name,attr"`
		Params  []param  `xml:"param"`
	}
	type include struct {
		XMLName xml.Name `xml:"include"`
		Gateway gateway  `xml:"gateway"`
	}

	proxy := fmt.Sprintf("%s:%d", c.Host, c.Port)
	transport := strings.ToLower(c.Transport)
	if transport == "" {
		transport = "udp"
	}
	register := "false"
	if c.Username != nil && *c.Username != "" {
		register = "true"
	}
	params := []param{
		{Name: "proxy", Value: proxy},
		{Name: "register", Value: register},
		{Name: "register-transport", Value: transport},
		{Name: "caller-id-in-from", Value: "true"},
	}
	if c.Username != nil && *c.Username != "" {
		params = append(params, param{Name: "username", Value: *c.Username})
	}
	if password != "" {
		params = append(params, param{Name: "password", Value: password})
	}
	if c.Realm != nil && *c.Realm != "" {
		params = append(params, param{Name: "realm", Value: *c.Realm})
	} else {
		params = append(params, param{Name: "realm", Value: c.Host})
	}

	doc := include{
		Gateway: gateway{
			Name:   GatewayName(c.ID),
			Params: params,
		},
	}
	out, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), out...), nil
}

// CarrierLoader loads carriers for gateway apply.
type CarrierLoader struct {
	DB *sql.DB
}

// ListOrdered returns all carriers ordered by priority ASC.
func (l *CarrierLoader) ListOrdered(ctx context.Context) ([]models.Carrier, error) {
	rows, err := l.DB.QueryContext(ctx, `
		SELECT id, name, host, port, transport, username, password_encrypted, realm,
		       codecs, caller_ids, max_cps, max_channels, enabled, priority, created_at
		FROM carriers
		ORDER BY priority ASC, created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Carrier
	for rows.Next() {
		c, err := scanCarrier(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type scannable interface {
	Scan(dest ...any) error
}

func scanCarrier(row scannable) (models.Carrier, error) {
	var c models.Carrier
	var username, realm sql.NullString
	var codecs, callerIDs pq.StringArray
	err := row.Scan(
		&c.ID, &c.Name, &c.Host, &c.Port, &c.Transport, &username, &c.PasswordEncrypted, &realm,
		&codecs, &callerIDs, &c.MaxCPS, &c.MaxChannels, &c.Enabled, &c.Priority, &c.CreatedAt,
	)
	if err != nil {
		return c, err
	}
	if username.Valid {
		c.Username = &username.String
	}
	if realm.Valid {
		c.Realm = &realm.String
	}
	c.Codecs = []string(codecs)
	c.CallerIDs = []string(callerIDs)
	return c, nil
}

// Reloader applies BYOC gateways from Postgres into FreeSWITCH.
type Reloader struct {
	ESL        APIClient
	GatewayDir string
	Loader     *CarrierLoader
	SecretKey  []byte
}

// ReloadAll decrypts and applies enabled carriers; removes disabled ones.
func (r *Reloader) ReloadAll(ctx context.Context) error {
	carriers, err := r.Loader.ListOrdered(ctx)
	if err != nil {
		return fmt.Errorf("list carriers: %w", err)
	}
	var firstErr error
	for _, c := range carriers {
		if !c.Enabled {
			if err := RemoveCarrierGateway(r.ESL, r.GatewayDir, c.ID); err != nil && firstErr == nil {
				firstErr = err
			}
			continue
		}
		password := ""
		if len(c.PasswordEncrypted) > 0 {
			pt, err := cryptokit.Decrypt(r.SecretKey, c.PasswordEncrypted)
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("decrypt carrier %s: %w", c.ID, err)
				}
				continue
			}
			password = string(pt)
		}
		if err := ApplyCarrierGateway(r.ESL, r.GatewayDir, c, password); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("apply carrier %s: %w", c.ID, err)
		}
	}
	return firstErr
}
