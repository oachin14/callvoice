package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/callvoice/callvoice/internal/models"
)

var ErrNotFound = errors.New("not found")

// CarrierStore persists BYOC carrier rows.
type CarrierStore struct {
	DB *sql.DB
}

type CreateCarrierInput struct {
	Name              string
	Host              string
	Port              int
	Transport         string
	Username          *string
	PasswordEncrypted []byte
	Realm             *string
	Codecs            []string
	CallerIDs         []string
	MaxCPS            int
	MaxChannels       int
	Enabled           bool
	Priority          int
}

type UpdateCarrierInput struct {
	Name              *string
	Host              *string
	Port              *int
	Transport         *string
	Username          *string
	ClearUsername     bool
	PasswordEncrypted []byte
	ClearPassword     bool
	Realm             *string
	ClearRealm        bool
	Codecs            []string
	CallerIDs         []string
	MaxCPS            *int
	MaxChannels       *int
	Enabled           *bool
	Priority          *int
}

func (s *CarrierStore) List(ctx context.Context) ([]models.Carrier, error) {
	rows, err := s.DB.QueryContext(ctx, `
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

func (s *CarrierStore) Get(ctx context.Context, id uuid.UUID) (*models.Carrier, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, name, host, port, transport, username, password_encrypted, realm,
		       codecs, caller_ids, max_cps, max_channels, enabled, priority, created_at
		FROM carriers WHERE id = $1
	`, id)
	c, err := scanCarrier(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *CarrierStore) Create(ctx context.Context, in CreateCarrierInput) (*models.Carrier, error) {
	if in.Port == 0 {
		in.Port = 5060
	}
	if in.Codecs == nil {
		in.Codecs = []string{"PCMU", "PCMA"}
	}
	if in.CallerIDs == nil {
		in.CallerIDs = []string{}
	}

	row := s.DB.QueryRowContext(ctx, `
		INSERT INTO carriers (
			name, host, port, transport, username, password_encrypted, realm,
			codecs, caller_ids, max_cps, max_channels, enabled, priority
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12, $13
		)
		RETURNING id, name, host, port, transport, username, password_encrypted, realm,
		          codecs, caller_ids, max_cps, max_channels, enabled, priority, created_at
	`,
		in.Name, in.Host, in.Port, in.Transport, in.Username, in.PasswordEncrypted, in.Realm,
		pq.Array(in.Codecs), pq.Array(in.CallerIDs), in.MaxCPS, in.MaxChannels, in.Enabled, in.Priority,
	)
	c, err := scanCarrier(row)
	if err != nil {
		return nil, fmt.Errorf("insert carrier: %w", err)
	}
	return &c, nil
}

func (s *CarrierStore) Update(ctx context.Context, id uuid.UUID, in UpdateCarrierInput) (*models.Carrier, error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	name := cur.Name
	if in.Name != nil {
		name = *in.Name
	}
	host := cur.Host
	if in.Host != nil {
		host = *in.Host
	}
	port := cur.Port
	if in.Port != nil {
		port = *in.Port
	}
	transport := cur.Transport
	if in.Transport != nil {
		transport = *in.Transport
	}
	username := cur.Username
	if in.ClearUsername {
		username = nil
	} else if in.Username != nil {
		username = in.Username
	}
	password := cur.PasswordEncrypted
	if in.ClearPassword {
		password = nil
	} else if in.PasswordEncrypted != nil {
		password = in.PasswordEncrypted
	}
	realm := cur.Realm
	if in.ClearRealm {
		realm = nil
	} else if in.Realm != nil {
		realm = in.Realm
	}
	codecs := cur.Codecs
	if in.Codecs != nil {
		codecs = in.Codecs
	}
	callerIDs := cur.CallerIDs
	if in.CallerIDs != nil {
		callerIDs = in.CallerIDs
	}
	maxCPS := cur.MaxCPS
	if in.MaxCPS != nil {
		maxCPS = *in.MaxCPS
	}
	maxChannels := cur.MaxChannels
	if in.MaxChannels != nil {
		maxChannels = *in.MaxChannels
	}
	enabled := cur.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	priority := cur.Priority
	if in.Priority != nil {
		priority = *in.Priority
	}

	row := s.DB.QueryRowContext(ctx, `
		UPDATE carriers SET
			name = $2, host = $3, port = $4, transport = $5,
			username = $6, password_encrypted = $7, realm = $8,
			codecs = $9, caller_ids = $10, max_cps = $11, max_channels = $12,
			enabled = $13, priority = $14
		WHERE id = $1
		RETURNING id, name, host, port, transport, username, password_encrypted, realm,
		          codecs, caller_ids, max_cps, max_channels, enabled, priority, created_at
	`,
		id, name, host, port, transport, username, password, realm,
		pq.Array(codecs), pq.Array(callerIDs), maxCPS, maxChannels, enabled, priority,
	)
	c, err := scanCarrier(row)
	if err != nil {
		return nil, fmt.Errorf("update carrier: %w", err)
	}
	return &c, nil
}

func (s *CarrierStore) Delete(ctx context.Context, id uuid.UUID) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM carriers WHERE id = $1`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanCarrier(row scannable) (models.Carrier, error) {
	var c models.Carrier
	var username, realm sql.NullString
	var codecs, callerIDs pq.StringArray
	var createdAt time.Time
	err := row.Scan(
		&c.ID, &c.Name, &c.Host, &c.Port, &c.Transport, &username, &c.PasswordEncrypted, &realm,
		&codecs, &callerIDs, &c.MaxCPS, &c.MaxChannels, &c.Enabled, &c.Priority, &createdAt,
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
	c.CreatedAt = createdAt
	return c, nil
}
