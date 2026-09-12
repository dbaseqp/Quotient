package middleware

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/dbaseqp/Quotient/www/api"
)

// load in authentication sources

// keycloak?
// ldap
// from config

// middleware requiring authentication to even hit
func Authentication(roles ...string) Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			username, user_roles := api.Authenticate(w, r)

			if username == "" {
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
			for _, user_role := range user_roles {
				if slices.Contains(roles, user_role) {
					ctx := context.WithValue(r.Context(), "username", username)
					ctx = context.WithValue(ctx, "roles", user_roles)
					next(w, r.WithContext(ctx))
					return
				}
			}
			api.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden"})
		}
	}
}
