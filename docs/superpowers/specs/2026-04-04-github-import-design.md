# GitHub Import Feature — Design Spec

**Date:** 2026-04-04
**Status:** Approved

## Overview

Allow users to import a GitHub repository into a Kumbula project. The workflow: create a project on the dashboard, provide a GitHub repo URL, click "Fetch & Deploy". The engine clones the repo from GitHub, pushes it to Gitea, and the existing webhook-based deploy pipeline handles the build. After import, the project is fully on Gitea and never references GitHub again.

## Decisions

- **GitHub auth:** Personal Access Token (PAT) stored per-user. Public repos work without a token; private repos require one with `repo` scope.
- **Token management:** User-level Settings page accessible from the sidebar footer.
- **Import UI:** A card on the project detail Overview tab, visible when the project is in `created` status and has no `github_url` set.
- **Import mechanism:** Server-side `git clone --mirror` from GitHub, then `git push --mirror` to Gitea. The Gitea webhook fires and triggers the normal deploy pipeline.
- **Deploy:** Immediate, triggered by the webhook after the push to Gitea. No separate deploy step.
- **One-shot:** After import, the project is fully on Gitea. No ongoing sync with GitHub. Nothing is deleted from GitHub.

## Database Changes

### `users` table

Add column:

```sql
ALTER TABLE users ADD COLUMN github_token TEXT NOT NULL DEFAULT '';
```

Stores the user's GitHub PAT in plaintext (consistent with existing `gitea_password` and `gitea_token` columns).

### `projects` table

Add column:

```sql
ALTER TABLE projects ADD COLUMN github_url TEXT NOT NULL DEFAULT '';
```

Records the source GitHub URL. Serves as a flag: if non-empty, the project was imported from GitHub. Displayed on the Overview tab for reference. Never used operationally after import.

## User Settings Page

### Routes

- `GET /settings` — render the settings page
- `POST /settings` — save GitHub token

### UI

A shell layout page (sidebar + topbar) with a single card:

- **GitHub Integration** card
  - "Personal Access Token" label
  - Password-type input, masked if a token exists
  - Help text with link to GitHub's token creation page, noting `repo` scope is needed
  - "Save" button
  - "Remove" button if a token is already saved

### Template

New file: `templates/settings.html`, registered in `shellPages` as `"settings"`.

### Sidebar

Add a "Settings" text link in the sidebar footer area, next to the logout button.

## Import Card on Project Detail

### Visibility

Shown on the Overview tab when:
- Project status is `created`
- Project `github_url` is empty (not yet imported)

Once import succeeds (or the user deploys via normal git push), the card disappears.

### UI

Card titled "Import from GitHub" containing:
- Text input for the GitHub repo URL (placeholder: `https://github.com/user/repo`)
- "Fetch & Deploy" submit button
- If the user has no GitHub PAT saved: helper text "For private repos, add a GitHub token in Settings" with a link to `/settings`

### After Import

The import card is replaced by a line under the project URL: "Imported from github.com/user/repo". The "Git Remote Setup" card remains — future updates go directly to Gitea.

## Import Handler

### Route

`POST /projects/{name}/import`

Added to `routeProject` as a new case for action `"import"`.

### Flow

1. CSRF validation
2. Parse and validate `github_url` — must start with `https://github.com/`
3. Load user record to get `github_token` (may be empty) and `gitea_password`
4. Update `github_url` on the project record in DB
5. Call `GitHubCloneAndPush()` (see below)
6. On success: redirect to project detail page (webhook fires, build starts)
7. On failure: clear `github_url` back to empty string in DB, redirect back with error message, project stays in `created` status so the user can retry

## GitHub Clone and Push Logic

### New file: `github.go`

Single exported function:

```
GitHubCloneAndPush(githubURL, githubToken, giteaRemoteURL, giteaUsername, giteaPassword string) error
```

Steps:
1. Build the clone URL — if `githubToken` is non-empty, inject it: `https://{token}@github.com/user/repo.git`
2. `git clone --mirror {cloneURL} {tempDir}` — gets all branches, tags, refs
3. Set the push remote: `git remote set-url origin {giteaURL}` with Gitea credentials embedded in the URL
4. `git push --mirror` to Gitea
5. Clean up temp directory
6. Return error if any step fails

### Error Cases

- Invalid URL format: rejected at handler level before cloning
- Clone fails (404, auth required): return error, handler shows "Could not access repository. Check the URL and ensure your GitHub token is set for private repos."
- Push to Gitea fails: return error, handler shows "Failed to push to Gitea. Please try again."

## What We Are NOT Building

- No GitHub OAuth or GitHub App registration
- No ongoing sync or pull from GitHub after import
- No branch selection (mirrors everything)
- No import from non-GitHub sources (GitLab, Bitbucket, etc.)
- No deletion of anything on GitHub
