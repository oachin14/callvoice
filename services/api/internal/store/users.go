package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/callvoice/callvoice/internal/models"
)

var ErrAdminExists = errors.New("admin already exists")
var ErrDuplicateEmail = errors.New("duplicate email")

// UserStore persists user rows.
type UserStore struct {
	DB *sql.DB
}

type CreateUserInput struct {
	Email        string
	PasswordHash string
	Role         models.UserRole
	DisplayName  *string
}

type UpdateUserInput struct {
	DisplayName *string
	Role        *models.UserRole
	Disabled    *bool
	DisabledAt  *time.Time
}

func (s *UserStore) List(ctx context.Context) ([]models.User, error) {
	rows, err := s.DB.QueryContext(ctx, userSelectSQL+` ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *UserStore) Get(ctx context.Context, id uuid.UUID) (*models.User, error) {
	row := s.DB.QueryRowContext(ctx, userSelectSQL+` WHERE id = $1`, id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *UserStore) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&n)
	return n, err
}

func (s *UserStore) Create(ctx context.Context, in CreateUserInput) (*models.User, error) {
	if in.Role == models.UserRoleAdmin {
		n, err := s.CountAdmins(ctx)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, ErrAdminExists
		}
	}

	row := s.DB.QueryRowContext(ctx, `
		INSERT INTO users (email, password_hash, role, display_name)
		VALUES (lower($1), $2, $3, $4)
		RETURNING `+userReturningCols,
		in.Email, in.PasswordHash, in.Role, in.DisplayName,
	)
	u, err := scanUser(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDuplicateEmail
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}
	return &u, nil
}

func (s *UserStore) Update(ctx context.Context, id uuid.UUID, in UpdateUserInput) (*models.User, error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	displayName := cur.DisplayName
	if in.DisplayName != nil {
		trimmed := strings.TrimSpace(*in.DisplayName)
		if trimmed == "" {
			displayName = nil
		} else {
			displayName = &trimmed
		}
	}

	role := cur.Role
	if in.Role != nil {
		if *in.Role == models.UserRoleAdmin && cur.Role != models.UserRoleAdmin {
			n, err := s.CountAdmins(ctx)
			if err != nil {
				return nil, err
			}
			if n > 0 {
				return nil, ErrAdminExists
			}
		}
		role = *in.Role
	}

	disabledAt := cur.DisabledAt
	if in.Disabled != nil {
		if *in.Disabled {
			if in.DisabledAt != nil {
				disabledAt = in.DisabledAt
			} else {
				now := time.Now().UTC()
				disabledAt = &now
			}
		} else {
			disabledAt = nil
		}
	}

	row := s.DB.QueryRowContext(ctx, `
		UPDATE users SET display_name = $2, role = $3, disabled_at = $4
		WHERE id = $1
		RETURNING `+userReturningCols,
		id, displayName, role, disabledAt,
	)
	u, err := scanUser(row)
	if err != nil {
		return nil, fmt.Errorf("update user: %w", err)
	}
	return &u, nil
}

func (s *UserStore) ResetPassword(ctx context.Context, id uuid.UUID, passwordHash string) error {
	res, err := s.DB.ExecContext(ctx, `
		UPDATE users SET password_hash = $2, failed_login_count = 0, locked_until = NULL
		WHERE id = $1
	`, id, passwordHash)
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

const userSelectSQL = `
	SELECT id, email, password_hash, role, display_name, totp_secret_encrypted, totp_enabled,
	       failed_login_count, locked_until, disabled_at, created_at
	FROM users`

const userReturningCols = `
	id, email, password_hash, role, display_name, totp_secret_encrypted, totp_enabled,
	failed_login_count, locked_until, disabled_at, created_at`

func scanUser(row scannable) (models.User, error) {
	var u models.User
	var displayName sql.NullString
	var lockedUntil, disabledAt sql.NullTime
	var createdAt time.Time
	err := row.Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.Role, &displayName, &u.TOTPSecretEncrypted, &u.TOTPEnabled,
		&u.FailedLoginCount, &lockedUntil, &disabledAt, &createdAt,
	)
	if err != nil {
		return u, err
	}
	if displayName.Valid {
		u.DisplayName = &displayName.String
	}
	if lockedUntil.Valid {
		u.LockedUntil = &lockedUntil.Time
	}
	if disabledAt.Valid {
		u.DisabledAt = &disabledAt.Time
	}
	u.CreatedAt = createdAt
	return u, nil
}

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}
