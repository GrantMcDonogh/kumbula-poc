package main

import "time"

type EnvVar struct {
	ID        int
	ProjectID int
	Key       string
	Value     string
	CreatedAt time.Time
}

func GetEnvVarsByProject(projectID int) ([]*EnvVar, error) {
	rows, err := DB.Query(
		`SELECT id, project_id, key, value, created_at
		 FROM project_env_vars WHERE project_id = $1 ORDER BY key`, projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var vars []*EnvVar
	for rows.Next() {
		v := &EnvVar{}
		if err := rows.Scan(&v.ID, &v.ProjectID, &v.Key, &v.Value, &v.CreatedAt); err != nil {
			return nil, err
		}
		vars = append(vars, v)
	}
	return vars, rows.Err()
}

func SetEnvVar(projectID int, key, value string) error {
	_, err := DB.Exec(
		`INSERT INTO project_env_vars (project_id, key, value)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (project_id, key) DO UPDATE SET value = EXCLUDED.value`,
		projectID, key, value,
	)
	return err
}

func DeleteEnvVar(envVarID int) error {
	_, err := DB.Exec(`DELETE FROM project_env_vars WHERE id = $1`, envVarID)
	return err
}
