package main

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// gitCmd creates a git command with credential helpers disabled to prevent
// system-level helpers (e.g. gh auth) from interfering.
func gitCmd(args ...string) *exec.Cmd {
	// Prepend -c credential.helper= to disable all credential helpers.
	fullArgs := append([]string{"-c", "credential.helper="}, args...)
	cmd := exec.Command("git", fullArgs...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd
}

// GitHubCloneAndPush clones a GitHub repo (mirror) and pushes all refs to Gitea.
// githubToken may be empty for public repos.
func GitHubCloneAndPush(githubURL, githubToken, giteaRemoteURL, giteaUsername, giteaPassword string) error {
	// Build authenticated GitHub clone URL (used for private repos)
	authCloneURL := githubURL
	if !strings.HasSuffix(authCloneURL, ".git") {
		authCloneURL += ".git"
	}
	if githubToken != "" {
		parsed, err := url.Parse(authCloneURL)
		if err != nil {
			return fmt.Errorf("parse github URL: %w", err)
		}
		parsed.User = url.UserPassword("x-access-token", githubToken)
		authCloneURL = parsed.String()
	}

	// Public clone URL (no credentials)
	publicURL := githubURL
	if !strings.HasSuffix(publicURL, ".git") {
		publicURL += ".git"
	}

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "kumbula-github-import-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Clone from GitHub — try without token first (works for public repos),
	// then retry with token if the first attempt fails and a token is available.
	log.Printf("  [github-import] Cloning %s ...", githubURL)
	cmd := gitCmd("clone", "--bare", publicURL, tmpDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		if githubToken == "" {
			return fmt.Errorf("git clone failed: %w\n%s", err, string(out))
		}
		// Retry with token for private repos
		log.Printf("  [github-import] Public clone failed, retrying with token...")
		os.RemoveAll(tmpDir)
		os.MkdirAll(tmpDir, 0755)
		cmd2 := gitCmd("clone", "--bare", authCloneURL, tmpDir)
		if out2, err2 := cmd2.CombinedOutput(); err2 != nil {
			return fmt.Errorf("git clone failed: %w\n%s", err2, string(out2))
		}
	}

	// Build authenticated Gitea push URL
	parsed, err := url.Parse(giteaRemoteURL)
	if err != nil {
		return fmt.Errorf("parse gitea URL: %w", err)
	}
	parsed.User = url.UserPassword(giteaUsername, giteaPassword)
	pushURL := parsed.String()

	// Set push remote and push mirror
	log.Printf("  [github-import] Pushing to Gitea ...")
	setRemote := gitCmd("-C", tmpDir, "remote", "set-url", "origin", pushURL)
	if out, err := setRemote.CombinedOutput(); err != nil {
		return fmt.Errorf("set remote failed: %w\n%s", err, string(out))
	}

	// Push branches and tags (not PR refs which Gitea rejects)
	push := gitCmd("-C", tmpDir, "push", "origin", "--all")
	if out, err := push.CombinedOutput(); err != nil {
		return fmt.Errorf("git push branches failed: %w\n%s", err, string(out))
	}
	pushTags := gitCmd("-C", tmpDir, "push", "origin", "--tags")
	if out, err := pushTags.CombinedOutput(); err != nil {
		log.Printf("  [github-import] Warning: push tags failed: %s", string(out))
	}

	log.Printf("  [github-import] Import complete.")
	return nil
}
