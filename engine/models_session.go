package main

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

type Session struct {
	Token     string
	UserID    int
	ExpiresAt time.Time
}

func CreateSession(userID int) (*Session, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(b)
	expiresAt := time.Now().Add(7 * 24 * time.Hour)

	_, err := DB.Exec(
		`INSERT INTO sessions (token, user_id, expires_at) VALUES ($1, $2, $3)`,
		token, userID, expiresAt,
	)
	if err != nil {
		return nil, err
	}
	return &Session{Token: token, UserID: userID, ExpiresAt: expiresAt}, nil
}

func GetSession(token string) (*Session, error) {
	s := &Session{}
	err := DB.QueryRow(
		`SELECT token, user_id, expires_at FROM sessions WHERE token = $1 AND expires_at > now()`,
		token,
	).Scan(&s.Token, &s.UserID, &s.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func DeleteSession(token string) error {
	_, err := DB.Exec(`DELETE FROM sessions WHERE token = $1`, token)
	return err
}

func CleanExpiredSessions() error {
	_, err := DB.Exec(`DELETE FROM sessions WHERE expires_at <= now()`)
	return err
}
