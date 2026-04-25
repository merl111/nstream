package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Role constants.
const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)

// User represents an nstream user account.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         string
	CreatedAt    time.Time
}

// Session represents an authenticated session.
type Session struct {
	Token     string
	UserID    int64
	ExpiresAt time.Time
}

// CreateUser inserts a new user with a bcrypt-hashed password.
func (d *DB) CreateUser(ctx context.Context, username, password, role string) (*User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("bcrypt: %w", err)
	}
	var u User
	err = d.QueryRowContext(ctx,
		`INSERT INTO users(username, password_hash, role) VALUES(?,?,?)
		 RETURNING id, username, password_hash, role, created_at`,
		username, string(hash), role,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return &u, nil
}

// GetUserByUsername fetches a user by username.
func (d *DB) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	var u User
	err := d.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, created_at FROM users WHERE username=?`,
		username,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUserByID fetches a user by primary key.
func (d *DB) GetUserByID(ctx context.Context, id int64) (*User, error) {
	var u User
	err := d.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, created_at FROM users WHERE id=?`, id,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// ListUsers returns all users ordered by username.
func (d *DB) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, username, password_hash, role, created_at FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// DeleteUser deletes a user by ID.
func (d *DB) DeleteUser(ctx context.Context, id int64) error {
	_, err := d.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id)
	return err
}

// CountUsers returns the total number of users.
func (d *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	return n, d.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
}

// Authenticate checks credentials; returns the user on success, nil on failure.
func (d *DB) Authenticate(ctx context.Context, username, password string) (*User, error) {
	u, err := d.GetUserByUsername(ctx, username)
	if err != nil || u == nil {
		return nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, nil
	}
	return u, nil
}

// CreateSession creates and stores a new session token for the user (24 hour TTL).
func (d *DB) CreateSession(ctx context.Context, userID int64) (*Session, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("rand: %w", err)
	}
	token := hex.EncodeToString(raw)
	exp := time.Now().Add(24 * time.Hour)
	_, err := d.ExecContext(ctx,
		`INSERT INTO sessions(token, user_id, expires_at) VALUES(?,?,?)`,
		token, userID, exp,
	)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return &Session{Token: token, UserID: userID, ExpiresAt: exp}, nil
}

// GetSession looks up a session by token, returning nil if not found or expired.
func (d *DB) GetSession(ctx context.Context, token string) (*Session, error) {
	var s Session
	err := d.QueryRowContext(ctx,
		`SELECT token, user_id, expires_at FROM sessions WHERE token=? AND expires_at > datetime('now')`,
		token,
	).Scan(&s.Token, &s.UserID, &s.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// DeleteSession removes a session token.
func (d *DB) DeleteSession(ctx context.Context, token string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM sessions WHERE token=?`, token)
	return err
}

// PruneExpiredSessions deletes sessions past their expiry.
func (d *DB) PruneExpiredSessions(ctx context.Context) error {
	_, err := d.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= datetime('now')`)
	return err
}
