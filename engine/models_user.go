package main

import (
	"time"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID            int
	Username      string
	Email         string
	PasswordHash  string
	GiteaPassword string
	GiteaToken    string
	GithubToken   string
	CreatedAt     time.Time
}

func CreateUser(username, email, password, giteaPassword string) (*User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, err
	}
	u := &User{}
	err = DB.QueryRow(
		`INSERT INTO users (username, email, password_hash, gitea_password)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, username, email, password_hash, gitea_password, gitea_token, github_token, created_at`,
		username, email, string(hash), giteaPassword,
	).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.GiteaPassword, &u.GiteaToken, &u.GithubToken, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func GetUserByUsername(username string) (*User, error) {
	u := &User{}
	err := DB.QueryRow(
		`SELECT id, username, email, password_hash, gitea_password, gitea_token, github_token, created_at
		 FROM users WHERE username = $1`, username,
	).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.GiteaPassword, &u.GiteaToken, &u.GithubToken, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func GetUserByID(id int) (*User, error) {
	u := &User{}
	err := DB.QueryRow(
		`SELECT id, username, email, password_hash, gitea_password, gitea_token, github_token, created_at
		 FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.GiteaPassword, &u.GiteaToken, &u.GithubToken, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func UpdateGiteaToken(userID int, token string) error {
	_, err := DB.Exec(`UPDATE users SET gitea_token = $1 WHERE id = $2`, token, userID)
	return err
}

func UpdateGithubToken(userID int, token string) error {
	_, err := DB.Exec(`UPDATE users SET github_token = $1 WHERE id = $2`, token, userID)
	return err
}

func CheckPassword(user *User, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	return err == nil
}
