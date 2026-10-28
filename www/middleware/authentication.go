package middleware

import (
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
func Authentication(a *api.API, roles ...string) Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			identity, ok := a.Authenticate(w, r)

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
					next(w, r.WithContext(api.WithIdentity(r.Context(), identity)))
					return
				}
			}
			api.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden"})
		}
	}
}
