package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		username, ok := r.Context().Value("username").(string)
		if !ok {
			username = "unknown"
		}
		slog.Info(fmt.Sprintf("%-7s %-20s %s", r.Method, r.URL.Path, time.Since(start)), "source", "web", "username", username)
	})
}
