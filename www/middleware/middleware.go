package middleware

import "net/http"

type Middleware func(http.HandlerFunc) http.HandlerFunc

func MiddlewareChain(middlewares ...Middleware) Middleware {
	return func(handler http.HandlerFunc) http.HandlerFunc {
		for _, mw := range middlewares {
			handler = mw(handler)
		}
		return handler
	}
}
