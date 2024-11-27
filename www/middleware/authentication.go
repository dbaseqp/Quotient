package middleware

import (
	"context"
	"encoding/json"
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
			username, user_roles := api.Authenticate(w, r)

			if username == "" {
				if slices.Contains(roles, "anonymous") {
					next(w, r)
					return
				}
				if strings.HasPrefix(r.URL.Path, "/api/") {
					w.WriteHeader(http.StatusUnauthorized)
					d, _ := json.Marshal(map[string]any{"error": "unauthorized"})
					w.Write(d)
					return
				}
				http.Redirect(w, r, "/login", http.StatusTemporaryRedirect)
				return
			}

			// need to refactor for multi-roles
			for _, user_role := range user_roles {
				for _, allowed_role := range roles {
					if user_role == allowed_role {
						ctx := context.WithValue(r.Context(), "username", username)
						ctx = context.WithValue(ctx, "roles", user_roles)
						next(w, r.WithContext(ctx))
						return
					}
				}
			}
			w.WriteHeader(http.StatusForbidden)
			d, _ := json.Marshal(map[string]any{"error": "forbidden"})
			w.Write(d)
		}
	}
}
