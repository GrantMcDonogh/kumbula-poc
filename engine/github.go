package main

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// GitHubCloneAndPush clones a GitHub repo (mirror) and pushes all refs to Gitea.
// githubToken may be empty for public repos.
func GitHubCloneAndPush(githubURL, githubToken, giteaRemoteURL, giteaUsername, giteaPassword string) error {
	// Build authenticated GitHub clone URL
	cloneURL := githubURL
	if !strings.HasSuffix(cloneURL, ".git") {
		cloneURL += ".git"
	}
	if githubToken != "" {
		parsed, err := url.Parse(cloneURL)
		if err != nil {
			return fmt.Errorf("parse github URL: %w", err)
		}
		parsed.User = url.UserPassword("x-access-token", githubToken)
		cloneURL = parsed.String()
	}

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "kumbula-github-import-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Clone mirror from GitHub
	log.Printf("  [github-import] Cloning %s ...", githubURL)
	cmd := exec.Command("git", "clone", "--mirror", cloneURL, tmpDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone failed: %w\n%s", err, string(out))
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
	setRemote := exec.Command("git", "-C", tmpDir, "remote", "set-url", "origin", pushURL)
	if out, err := setRemote.CombinedOutput(); err != nil {
		return fmt.Errorf("set remote failed: %w\n%s", err, string(out))
	}

	push := exec.Command("git", "-C", tmpDir, "push", "--mirror")
	if out, err := push.CombinedOutput(); err != nil {
		return fmt.Errorf("git push failed: %w\n%s", err, string(out))
	}

	log.Printf("  [github-import] Import complete.")
	return nil
}
