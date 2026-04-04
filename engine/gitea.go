package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

var GiteaAdminToken string
var GiteaURL string

func InitGitea() {
	GiteaAdminToken = os.Getenv("GITEA_ADMIN_TOKEN")
	if GiteaAdminToken == "" {
		GiteaAdminToken = os.Getenv("GITEA_TOKEN")
	}

	ip := getContainerIP("gitea")
	if ip != "" {
		GiteaURL = fmt.Sprintf("http://%s:3000", ip)
	} else {
		GiteaURL = fmt.Sprintf("http://%s", GITEA_DOMAIN)
	}

	log.Printf("Gitea API: %s (token=%v)", GiteaURL, GiteaAdminToken != "")
}

func giteaRequest(method, path, token string, body interface{}) ([]byte, int, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	url := GiteaURL + path
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}

	return data, resp.StatusCode, nil
}

func GiteaCreateUser(username, email, password string) error {
	payload := map[string]interface{}{
		"username":             username,
		"email":                email,
		"password":             password,
		"must_change_password": false,
	}

	data, status, err := giteaRequest("POST", "/api/v1/admin/users", GiteaAdminToken, payload)
	if err != nil {
		return fmt.Errorf("create user request: %w", err)
	}
	if status != 201 {
		return fmt.Errorf("create user failed (status %d): %s", status, string(data))
	}

	log.Printf("Gitea: created user %s", username)
	return nil
}

func GiteaCreateToken(username, password string) (string, error) {
	payload := map[string]interface{}{
		"name": "kumbula-token",
		"scopes": []string{
			"write:repository",
			"write:user",
		},
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal body: %w", err)
	}

	url := GiteaURL + fmt.Sprintf("/api/v1/users/%s/tokens", username)
	req, err := http.NewRequest("POST", url, bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(username, password)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 201 {
		return "", fmt.Errorf("create token failed (status %d): %s", resp.StatusCode, string(data))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	sha1, ok := result["sha1"].(string)
	if !ok {
		return "", fmt.Errorf("no sha1 in token response")
	}

	log.Printf("Gitea: created token for user %s", username)
	return sha1, nil
}

func GiteaCreateRepo(userToken, repoName string) (string, error) {
	payload := map[string]interface{}{
		"name":          repoName,
		"auto_init":     true,
		"default_branch": "main",
	}

	data, status, err := giteaRequest("POST", "/api/v1/user/repos", userToken, payload)
	if err != nil {
		return "", fmt.Errorf("create repo request: %w", err)
	}
	if status != 201 {
		return "", fmt.Errorf("create repo failed (status %d): %s", status, string(data))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	cloneURL, ok := result["clone_url"].(string)
	if !ok {
		return "", fmt.Errorf("no clone_url in repo response")
	}

	log.Printf("Gitea: created repo %s", repoName)
	return cloneURL, nil
}

func GiteaAddWebhook(userToken, owner, repoName, webhookURL string) error {
	payload := map[string]interface{}{
		"type":   "gitea",
		"active": true,
		"config": map[string]string{
			"url":          webhookURL,
			"content_type": "json",
		},
		"events": []string{"push"},
	}

	path := fmt.Sprintf("/api/v1/repos/%s/%s/hooks", owner, repoName)
	data, status, err := giteaRequest("POST", path, userToken, payload)
	if err != nil {
		return fmt.Errorf("add webhook request: %w", err)
	}
	if status != 201 {
		return fmt.Errorf("add webhook failed (status %d): %s", status, string(data))
	}

	log.Printf("Gitea: added webhook to %s/%s -> %s", owner, repoName, webhookURL)
	return nil
}

func GiteaDeleteRepo(userToken, owner, repoName string) error {
	path := fmt.Sprintf("/api/v1/repos/%s/%s", owner, repoName)
	data, status, err := giteaRequest("DELETE", path, userToken, nil)
	if err != nil {
		return fmt.Errorf("delete repo request: %w", err)
	}
	if status != 204 {
		return fmt.Errorf("delete repo failed (status %d): %s", status, string(data))
	}

	log.Printf("Gitea: deleted repo %s/%s", owner, repoName)
	return nil
}
