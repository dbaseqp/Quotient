package middleware

import (
	"context"
	"net/http"
	"quotient/www/api"
	"slices"
	"strings"
)

// load in authentication sources

// keycloak?
// ldap
// from config

// middleware requiring authentication to even hit
func Authentication(roles ...string) Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			identity, ok := api.Authenticate(w, r)

			if !ok {
				if slices.Contains(roles, "anonymous") {
					next(w, r)
					return
				}
				if strings.HasPrefix(r.URL.Path, "/api/") {
					api.WriteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
					return
				}
				http.Redirect(w, r, "/login", http.StatusTemporaryRedirect)
				return
			}

			// need to refactor for multi-roles
			for _, user_role := range identity.Roles {
				if slices.Contains(roles, user_role) {
					// Identity is authoritative; the username and roles values
					// are the same data under the keys handlers already read.
					ctx := api.WithIdentity(r.Context(), identity)
					ctx = context.WithValue(ctx, "username", identity.Username)
					ctx = context.WithValue(ctx, "roles", identity.Roles)
					next(w, r.WithContext(ctx))
					return
				}
			}
			api.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden"})
		}
	}
}
