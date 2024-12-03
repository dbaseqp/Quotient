package middleware

import "net/http"

// Middleware represents a function that wraps an HTTP handler with additional behavior.
type Middleware func(http.HandlerFunc) http.HandlerFunc

// Chain applies a series of middlewares to an HTTP handler.
func Chain(middlewares ...Middleware) Middleware {
	return func(handler http.HandlerFunc) http.HandlerFunc {
		for _, mw := range middlewares {
			handler = mw(handler)
		}
		return handler
	}
}
