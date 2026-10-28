package middleware

import (
	"crypto/rand"
	"log/slog"
	"net/http"
	"time"

	"github.com/dbaseqp/Quotient/www/api"
)

// Logging middleware logs quotient web requests. It is meant to be called after authentication is performed
func Logging(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Generate (or reuse) a request ID.
		requestID := api.RequestID(r.Context())
		if requestID == "" {
			// Short enough to keep log lines readable; only needs to tell nearby requests apart.
			requestID = rand.Text()[:8]
			r = r.WithContext(api.WithRequestID(r.Context(), requestID))
		}

		// Process the request.
		next.ServeHTTP(w, r)
		duration := time.Since(start)

		// Determine the client IP.
		clientIP := r.RemoteAddr
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			clientIP = forwarded
		}

		// Retrieve the username safely from the context.
		username, ok := r.Context().Value("username").(string)
		if !ok {
			username = "anonymous"
		}

		// Log the request details.
		slog.Info("HTTP request completed",
			"request_id", requestID,
			"client_ip", clientIP,
			"user_id", username,
			"method", r.Method,
			"uri", r.RequestURI,
			"protocol", r.Proto,
			"duration", duration,
			"referer", r.Referer(),
			"user_agent", r.UserAgent(),
		)
	}
}
