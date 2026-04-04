package main

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"time"
)

var usernameRegex = regexp.MustCompile(`^[a-z][a-z0-9-]{1,48}[a-z0-9]$`)

var reservedNames = map[string]bool{
	"traefik":   true,
	"gitea":     true,
	"dashboard": true,
	"postgres":  true,
	"api":       true,
	"admin":     true,
	"www":       true,
	"new":       true,
	"login":     true,
	"signup":    true,
	"logout":    true,
	"static":    true,
	"health":    true,
	"webhook":   true,
	"apps":      true,
	"partials":  true,
	"projects":  true,
}

// handleLoginPage renders the login form. If the user is already logged in, redirect to /.
func handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if CtxUser(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	RenderAuth(w, r, "login", nil)
}

// handleLogin processes the login form submission.
func handleLogin(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")

	user, err := GetUserByUsername(username)
	if err != nil || !CheckPassword(user, password) {
		RenderAuth(w, r, "login", map[string]interface{}{
			"Error": "Invalid username or password.",
		})
		return
	}

	session, err := CreateSession(user.ID)
	if err != nil {
		log.Printf("auth: failed to create session for %s: %v", username, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    session.Token,
		Path:     "/",
		MaxAge:   7 * 24 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleSignupPage renders the signup form. If the user is already logged in, redirect to /.
func handleSignupPage(w http.ResponseWriter, r *http.Request) {
	if CtxUser(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	RenderAuth(w, r, "signup", nil)
}

// handleSignup processes the signup form submission.
func handleSignup(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	email := r.FormValue("email")
	password := r.FormValue("password")

	// Validate username
	if err := ValidateProjectName(username); err != nil {
		RenderAuth(w, r, "signup", map[string]interface{}{
			"Error":   err.Error(),
		})
		return
	}

	// Validate password
	if len(password) < 8 {
		RenderAuth(w, r, "signup", map[string]interface{}{
			"Error":   "Password must be at least 8 characters.",
		})
		return
	}

	// Generate a separate Gitea password for the user
	giteaPass := generatePassword(24)

	// Create KumbulaCloud user
	user, err := CreateUser(username, email, password, giteaPass)
	if err != nil {
		log.Printf("auth: failed to create user %s: %v", username, err)
		RenderAuth(w, r, "signup", map[string]interface{}{
			"Error":   "Username or email already taken.",
		})
		return
	}

	// Create Gitea user
	if err := GiteaCreateUser(username, email, giteaPass); err != nil {
		log.Printf("auth: failed to create Gitea user %s: %v", username, err)
		// Rollback: delete the KumbulaCloud user
		DB.Exec(`DELETE FROM users WHERE id = $1`, user.ID)
		RenderAuth(w, r, "signup", map[string]interface{}{
			"Error":   "Failed to provision account. Please try again.",
		})
		return
	}

	// Create Gitea token
	token, err := GiteaCreateToken(username, giteaPass)
	if err != nil {
		log.Printf("auth: failed to create Gitea token for %s: %v", username, err)
		// Rollback: delete the KumbulaCloud user (Gitea user is orphaned but non-critical)
		DB.Exec(`DELETE FROM users WHERE id = $1`, user.ID)
		RenderAuth(w, r, "signup", map[string]interface{}{
			"Error":   "Failed to provision account. Please try again.",
		})
		return
	}

	// Store the Gitea token
	if err := UpdateGiteaToken(user.ID, token); err != nil {
		log.Printf("auth: failed to store Gitea token for %s: %v", username, err)
	}

	// Create session
	session, err := CreateSession(user.ID)
	if err != nil {
		log.Printf("auth: failed to create session for %s: %v", username, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    session.Token,
		Path:     "/",
		MaxAge:   7 * 24 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleLogout deletes the session and clears the cookie.
func handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		DeleteSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Expires:  time.Unix(0, 0),
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ValidateProjectName checks that a name matches the allowed pattern and is not reserved.
func ValidateProjectName(name string) error {
	if !usernameRegex.MatchString(name) {
		return fmt.Errorf("Name must be 3-50 lowercase letters, numbers, or hyphens, starting with a letter.")
	}
	if reservedNames[name] {
		return fmt.Errorf("The name %q is reserved.", name)
	}
	return nil
}
