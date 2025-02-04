package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Logging middleware logs quotient web requests. It is meant to be called after authentication is performed
func Logging(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Generate (or reuse) a unique request ID.
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}
		w.Header().Set("X-Request-ID", requestID)

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
