/*
Package middleware provides reusable HTTP middleware components for the application.

This package includes functionality such as authentication, logging, and other
common middleware patterns to be used across the application.
*/
package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"quotient/www/api"
	"slices"
	"strings"
)

type contextKey string

const (
	usernameKey contextKey = "username"
	rolesKey    contextKey = "roles"
)

// load in authentication sources

// keycloak?
// ldap
// from config

/*
Authentication is a middleware function that ensures the user is authenticated
and has the required roles to access the endpoint. If the user is not authenticated
or does not have the necessary roles, it returns an appropriate HTTP status code.
*/
func Authentication(roles ...string) Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			username, userRoles := api.Authenticate(w, r)

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
			for _, userRole := range userRoles {
				for _, allowedRole := range roles {
					if userRole == allowedRole {
						ctx := context.WithValue(r.Context(), contextKey(usernameKey), username)
						ctx = context.WithValue(ctx, contextKey(rolesKey), userRoles)
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
