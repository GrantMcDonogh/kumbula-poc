package main

import "database/sql"

type Build struct {
	ID         int
	ProjectID  int
	Status     string
	Log        string
	CommitSHA  string
	StartedAt  sql.NullTime
	FinishedAt sql.NullTime
}

func CreateBuild(projectID int, commitSHA string) (*Build, error) {
	b := &Build{}
	err := DB.QueryRow(
		`INSERT INTO builds (project_id, commit_sha, status, started_at)
		 VALUES ($1, $2, 'building', now())
		 RETURNING id, project_id, status, log, commit_sha, started_at, finished_at`,
		projectID, commitSHA,
	).Scan(&b.ID, &b.ProjectID, &b.Status, &b.Log, &b.CommitSHA, &b.StartedAt, &b.FinishedAt)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func GetBuildsByProject(projectID int) ([]*Build, error) {
	rows, err := DB.Query(
		`SELECT id, project_id, status, log, commit_sha, started_at, finished_at
		 FROM builds WHERE project_id = $1 ORDER BY id DESC`, projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var builds []*Build
	for rows.Next() {
		b := &Build{}
		if err := rows.Scan(&b.ID, &b.ProjectID, &b.Status, &b.Log, &b.CommitSHA, &b.StartedAt, &b.FinishedAt); err != nil {
			return nil, err
		}
		builds = append(builds, b)
	}
	return builds, rows.Err()
}

func GetBuild(buildID int) (*Build, error) {
	b := &Build{}
	err := DB.QueryRow(
		`SELECT id, project_id, status, log, commit_sha, started_at, finished_at
		 FROM builds WHERE id = $1`, buildID,
	).Scan(&b.ID, &b.ProjectID, &b.Status, &b.Log, &b.CommitSHA, &b.StartedAt, &b.FinishedAt)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func AppendBuildLog(buildID int, text string) error {
	_, err := DB.Exec(`UPDATE builds SET log = log || $1 WHERE id = $2`, text, buildID)
	return err
}

func FinishBuild(buildID int, status string) error {
	_, err := DB.Exec(
		`UPDATE builds SET status = $1, finished_at = now() WHERE id = $2`,
		status, buildID,
	)
	return err
}
