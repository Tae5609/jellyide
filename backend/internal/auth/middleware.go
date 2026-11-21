package auth

import (
	"context"
	"net/http"
	"strings"
)

// userContextKey is the context key under which the authenticated user is
// stored by the middleware.
type userContextKey struct{}

// isPublicPath reports whether the path needs no session at all.
//
// Public: anything outside /api/ and /media/, plus /api/auth/* except
// /api/auth/me, /api/auth/logout and /api/auth/password.
func isPublicPath(path string) bool {
	if !strings.HasPrefix(path, "/api/") && !strings.HasPrefix(path, "/media/") {
		return true
	}
	if strings.HasPrefix(path, "/api/auth/") {
		switch path {
		case "/api/auth/me", "/api/auth/logout", "/api/auth/password":
			return false
		}
		return true
	}
	return false
}

// Middleware enforces authentication on protected paths. When auth is
// disabled it passes everything through. Protected state-changing requests
// (POST/PUT/PATCH/DELETE) must carry the X-Requested-With: XMLHttpRequest
// header (CSRF protection).
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.Enabled || isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		user, err := s.UserFromRequest(r)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				writeJSONError(w, http.StatusForbidden, "csrf")
				return
			}
		}

		ctx := context.WithValue(r.Context(), userContextKey{}, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// UserFromContext returns the authenticated user stored by Middleware, or
// nil if the request is not authenticated.
func UserFromContext(ctx context.Context) *User {
	u, _ := ctx.Value(userContextKey{}).(*User)
	return u
}
