package main

import "log"

func RunMigrations() error {
	tables := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id            SERIAL PRIMARY KEY,
			username      TEXT NOT NULL UNIQUE,
			email         TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			gitea_password TEXT NOT NULL DEFAULT '',
			gitea_token    TEXT NOT NULL DEFAULT '',
			created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS projects (
			id           SERIAL PRIMARY KEY,
			user_id      INTEGER NOT NULL REFERENCES users(id),
			name         TEXT NOT NULL UNIQUE,
			gitea_repo   TEXT NOT NULL DEFAULT '',
			container_id TEXT,
			status       TEXT NOT NULL DEFAULT 'created',
			url          TEXT NOT NULL DEFAULT '',
			database_url TEXT,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS builds (
			id          SERIAL PRIMARY KEY,
			project_id  INTEGER NOT NULL REFERENCES projects(id),
			status      TEXT NOT NULL DEFAULT 'pending',
			log         TEXT NOT NULL DEFAULT '',
			commit_sha  TEXT NOT NULL DEFAULT '',
			started_at  TIMESTAMPTZ,
			finished_at TIMESTAMPTZ
		)`,
		`CREATE TABLE IF NOT EXISTS project_env_vars (
			id         SERIAL PRIMARY KEY,
			project_id INTEGER NOT NULL REFERENCES projects(id),
			key        TEXT NOT NULL,
			value      TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE(project_id, key)
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token      TEXT PRIMARY KEY,
			user_id    INTEGER NOT NULL REFERENCES users(id),
			expires_at TIMESTAMPTZ NOT NULL
		)`,
		// --- GitHub import columns ---
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS github_token TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE projects ADD COLUMN IF NOT EXISTS github_url TEXT NOT NULL DEFAULT ''`,
	}

	for _, ddl := range tables {
		if _, err := DB.Exec(ddl); err != nil {
			return err
		}
	}

	log.Printf("Database migrations complete")
	return nil
}
