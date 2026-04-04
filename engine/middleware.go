package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
)

const sessionCookieName = "kumbula_session"

// SessionMiddleware reads the session cookie, loads the user, and attaches
// the user and a CSRF token (first 32 chars of session token) to the context.
func SessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		session, err := GetSession(cookie.Value)
		if err != nil {
			// Invalid or expired session — clear the cookie.
			http.SetCookie(w, &http.Cookie{
				Name:     sessionCookieName,
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				HttpOnly: true,
			})
			next.ServeHTTP(w, r)
			return
		}

		user, err := GetUserByID(session.UserID)
		if err != nil {
			// User no longer exists — clear the cookie.
			http.SetCookie(w, &http.Cookie{
				Name:     sessionCookieName,
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				HttpOnly: true,
			})
			next.ServeHTTP(w, r)
			return
		}

		// Derive CSRF token from the first 32 characters of the session token.
		csrf := session.Token
		if len(csrf) > 32 {
			csrf = csrf[:32]
		}

		r = withUser(r, user)
		r = withCSRF(r, csrf)
		next.ServeHTTP(w, r)
	})
}

// RequireAuth redirects unauthenticated requests to /login.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if CtxUser(r) == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CSRFProtect validates the CSRF token on state-changing requests (POST, DELETE).
func CSRFProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodDelete {
			expected := CtxCSRF(r)
			token := r.FormValue("_csrf")
			if token == "" {
				token = r.Header.Get("X-CSRF-Token")
			}
			if expected == "" || token != expected {
				http.Error(w, "Forbidden — invalid CSRF token", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequireProjectOwner extracts the project name from the URL, loads the
// project, verifies ownership, and attaches it to the context. Returns 404
// if the project is not found or the user is not the owner.
func RequireProjectOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Expect URL pattern: /projects/{name}/...
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 2 {
			http.NotFound(w, r)
			return
		}
		name := parts[1]

		// Skip ownership check for the "new" pseudo-route.
		if name == "new" {
			next.ServeHTTP(w, r)
			return
		}

		project, err := GetProjectByName(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		user := CtxUser(r)
		if user == nil || project.UserID != user.ID {
			http.NotFound(w, r)
			return
		}

		r = withProject(r, project)
		next.ServeHTTP(w, r)
	})
}

// generateCSRFToken returns a 32-character hex string from 16 random bytes.
func generateCSRFToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
