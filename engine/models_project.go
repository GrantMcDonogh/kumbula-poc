package main

import (
	"database/sql"
	"time"
)

type Project struct {
	ID          int
	UserID      int
	Name        string
	GiteaRepo   string
	ContainerID sql.NullString
	Status      string
	URL         string
	DatabaseURL sql.NullString
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func CreateProject(userID int, name, giteaRepo, url string) (*Project, error) {
	p := &Project{}
	err := DB.QueryRow(
		`INSERT INTO projects (user_id, name, gitea_repo, url)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, user_id, name, gitea_repo, container_id, status, url, database_url, created_at, updated_at`,
		userID, name, giteaRepo, url,
	).Scan(&p.ID, &p.UserID, &p.Name, &p.GiteaRepo, &p.ContainerID, &p.Status, &p.URL, &p.DatabaseURL, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func GetProjectsByUser(userID int) ([]*Project, error) {
	rows, err := DB.Query(
		`SELECT id, user_id, name, gitea_repo, container_id, status, url, database_url, created_at, updated_at
		 FROM projects WHERE user_id = $1 ORDER BY created_at DESC`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []*Project
	for rows.Next() {
		p := &Project{}
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.GiteaRepo, &p.ContainerID, &p.Status, &p.URL, &p.DatabaseURL, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func GetProjectByName(name string) (*Project, error) {
	p := &Project{}
	err := DB.QueryRow(
		`SELECT id, user_id, name, gitea_repo, container_id, status, url, database_url, created_at, updated_at
		 FROM projects WHERE name = $1`, name,
	).Scan(&p.ID, &p.UserID, &p.Name, &p.GiteaRepo, &p.ContainerID, &p.Status, &p.URL, &p.DatabaseURL, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func UpdateProjectStatus(projectID int, status, containerID string) error {
	_, err := DB.Exec(
		`UPDATE projects SET status = $1, container_id = $2, updated_at = now() WHERE id = $3`,
		status, containerID, projectID,
	)
	return err
}

func UpdateProjectDatabaseURL(projectID int, dbURL string) error {
	_, err := DB.Exec(
		`UPDATE projects SET database_url = $1, updated_at = now() WHERE id = $2`,
		dbURL, projectID,
	)
	return err
}

func DeleteProject(projectID int) error {
	_, err := DB.Exec(`DELETE FROM projects WHERE id = $1`, projectID)
	return err
}
