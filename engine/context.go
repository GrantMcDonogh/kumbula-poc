package main

import (
	"context"
	"net/http"
)

type contextKey string

const (
	ctxUserKey    contextKey = "user"
	ctxProjectKey contextKey = "project"
	ctxCSRFKey    contextKey = "csrf"
)

func CtxUser(r *http.Request) *User {
	u, _ := r.Context().Value(ctxUserKey).(*User)
	return u
}

func CtxProject(r *http.Request) *Project {
	p, _ := r.Context().Value(ctxProjectKey).(*Project)
	return p
}

func CtxCSRF(r *http.Request) string {
	s, _ := r.Context().Value(ctxCSRFKey).(string)
	return s
}

func withUser(r *http.Request, u *User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxUserKey, u))
}

func withProject(r *http.Request, p *Project) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxProjectKey, p))
}

func withCSRF(r *http.Request, token string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxCSRFKey, token))
}
