package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"testing"

	_ "github.com/lib/pq"
)

func TestMain(m *testing.M) {
	connStr := fmt.Sprintf("host=%s port=5432 user=%s password=%s dbname=kumbula_system sslmode=disable",
		POSTGRES_HOST, POSTGRES_USER, POSTGRES_PASS)
	var err error
	DB, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("test db open: %v", err)
	}
	if err := DB.Ping(); err != nil {
		log.Fatalf("test db ping: %v", err)
	}
	if err := RunMigrations(); err != nil {
		log.Fatalf("test migrations: %v", err)
	}

	code := m.Run()

	// Cleanup in reverse FK order
	DB.Exec("DELETE FROM project_env_vars")
	DB.Exec("DELETE FROM builds")
	DB.Exec("DELETE FROM sessions")
	DB.Exec("DELETE FROM projects")
	DB.Exec("DELETE FROM users")
	DB.Close()

	os.Exit(code)
}

func cleanAll(t *testing.T) {
	t.Helper()
	for _, tbl := range []string{"project_env_vars", "builds", "sessions", "projects", "users"} {
		if _, err := DB.Exec("DELETE FROM " + tbl); err != nil {
			t.Fatalf("clean %s: %v", tbl, err)
		}
	}
}

func TestCreateAndGetUser(t *testing.T) {
	cleanAll(t)

	u, err := CreateUser("alice", "alice@example.com", "secret123", "gitea_pass")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.ID == 0 {
		t.Fatal("expected non-zero user ID")
	}
	if u.Username != "alice" {
		t.Fatalf("expected username alice, got %s", u.Username)
	}

	// GetByUsername
	u2, err := GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if u2.ID != u.ID {
		t.Fatalf("ID mismatch: %d vs %d", u2.ID, u.ID)
	}

	// GetByID
	u3, err := GetUserByID(u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if u3.Username != "alice" {
		t.Fatalf("expected alice, got %s", u3.Username)
	}

	// UpdateGiteaToken
	if err := UpdateGiteaToken(u.ID, "tok_abc"); err != nil {
		t.Fatalf("UpdateGiteaToken: %v", err)
	}
	u4, _ := GetUserByID(u.ID)
	if u4.GiteaToken != "tok_abc" {
		t.Fatalf("expected tok_abc, got %s", u4.GiteaToken)
	}
}

func TestCheckPassword(t *testing.T) {
	cleanAll(t)

	u, err := CreateUser("bob", "bob@example.com", "mypassword", "gp")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if !CheckPassword(u, "mypassword") {
		t.Fatal("expected password to match")
	}
	if CheckPassword(u, "wrongpassword") {
		t.Fatal("expected password to not match")
	}
}

func TestSessionLifecycle(t *testing.T) {
	cleanAll(t)

	u, err := CreateUser("carol", "carol@example.com", "pass", "gp")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	s, err := CreateSession(u.ID)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if len(s.Token) != 64 { // 32 bytes hex = 64 chars
		t.Fatalf("expected 64-char token, got %d", len(s.Token))
	}

	s2, err := GetSession(s.Token)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if s2.UserID != u.ID {
		t.Fatalf("session user mismatch")
	}

	if err := DeleteSession(s.Token); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	_, err = GetSession(s.Token)
	if err == nil {
		t.Fatal("expected error after delete")
	}

	// CleanExpiredSessions should not error
	if err := CleanExpiredSessions(); err != nil {
		t.Fatalf("CleanExpiredSessions: %v", err)
	}
}

func TestProjectCRUD(t *testing.T) {
	cleanAll(t)

	u, err := CreateUser("dave", "dave@example.com", "pass", "gp")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	p, err := CreateProject(u.ID, "myapp", "dave/myapp", "http://myapp.kumbula.local")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.Status != "created" {
		t.Fatalf("expected status created, got %s", p.Status)
	}

	// GetByUser
	projects, err := GetProjectsByUser(u.ID)
	if err != nil {
		t.Fatalf("GetProjectsByUser: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}

	// GetByName
	p2, err := GetProjectByName("myapp")
	if err != nil {
		t.Fatalf("GetProjectByName: %v", err)
	}
	if p2.ID != p.ID {
		t.Fatal("project ID mismatch")
	}

	// UpdateStatus
	if err := UpdateProjectStatus(p.ID, "running", "cid_123"); err != nil {
		t.Fatalf("UpdateProjectStatus: %v", err)
	}
	p3, _ := GetProjectByName("myapp")
	if p3.Status != "running" {
		t.Fatalf("expected running, got %s", p3.Status)
	}

	// UpdateDatabaseURL
	if err := UpdateProjectDatabaseURL(p.ID, "postgres://localhost/myapp"); err != nil {
		t.Fatalf("UpdateProjectDatabaseURL: %v", err)
	}
	p4, _ := GetProjectByName("myapp")
	if p4.DatabaseURL.String != "postgres://localhost/myapp" {
		t.Fatalf("expected db url, got %s", p4.DatabaseURL.String)
	}

	// Delete
	if err := DeleteProject(p.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	_, err = GetProjectByName("myapp")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestBuildLifecycle(t *testing.T) {
	cleanAll(t)

	u, _ := CreateUser("eve", "eve@example.com", "pass", "gp")
	p, _ := CreateProject(u.ID, "buildapp", "eve/buildapp", "http://buildapp.kumbula.local")

	b, err := CreateBuild(p.ID, "abc123")
	if err != nil {
		t.Fatalf("CreateBuild: %v", err)
	}
	if b.Status != "building" {
		t.Fatalf("expected building, got %s", b.Status)
	}

	// AppendLog
	if err := AppendBuildLog(b.ID, "step 1 done\n"); err != nil {
		t.Fatalf("AppendBuildLog: %v", err)
	}
	if err := AppendBuildLog(b.ID, "step 2 done\n"); err != nil {
		t.Fatalf("AppendBuildLog: %v", err)
	}

	b2, err := GetBuild(b.ID)
	if err != nil {
		t.Fatalf("GetBuild: %v", err)
	}
	if b2.Log != "step 1 done\nstep 2 done\n" {
		t.Fatalf("unexpected log: %q", b2.Log)
	}

	// Finish
	if err := FinishBuild(b.ID, "success"); err != nil {
		t.Fatalf("FinishBuild: %v", err)
	}
	b3, _ := GetBuild(b.ID)
	if b3.Status != "success" {
		t.Fatalf("expected success, got %s", b3.Status)
	}
	if !b3.FinishedAt.Valid {
		t.Fatal("expected finished_at to be set")
	}

	// GetByProject
	builds, err := GetBuildsByProject(p.ID)
	if err != nil {
		t.Fatalf("GetBuildsByProject: %v", err)
	}
	if len(builds) != 1 {
		t.Fatalf("expected 1 build, got %d", len(builds))
	}
}

func TestEnvVarCRUD(t *testing.T) {
	cleanAll(t)

	u, _ := CreateUser("frank", "frank@example.com", "pass", "gp")
	p, _ := CreateProject(u.ID, "envapp", "frank/envapp", "http://envapp.kumbula.local")

	// Set
	if err := SetEnvVar(p.ID, "DATABASE_URL", "postgres://localhost/envapp"); err != nil {
		t.Fatalf("SetEnvVar: %v", err)
	}
	if err := SetEnvVar(p.ID, "SECRET_KEY", "abc"); err != nil {
		t.Fatalf("SetEnvVar: %v", err)
	}

	vars, err := GetEnvVarsByProject(p.ID)
	if err != nil {
		t.Fatalf("GetEnvVarsByProject: %v", err)
	}
	if len(vars) != 2 {
		t.Fatalf("expected 2 vars, got %d", len(vars))
	}

	// Upsert
	if err := SetEnvVar(p.ID, "SECRET_KEY", "xyz"); err != nil {
		t.Fatalf("SetEnvVar upsert: %v", err)
	}
	vars2, _ := GetEnvVarsByProject(p.ID)
	found := false
	for _, v := range vars2 {
		if v.Key == "SECRET_KEY" && v.Value == "xyz" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected upserted value xyz")
	}

	// Delete
	if err := DeleteEnvVar(vars2[0].ID); err != nil {
		t.Fatalf("DeleteEnvVar: %v", err)
	}
	vars3, _ := GetEnvVarsByProject(p.ID)
	if len(vars3) != 1 {
		t.Fatalf("expected 1 var after delete, got %d", len(vars3))
	}
}
