package middleware

import "net/http"

func Cors(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")                            // Allows all origins
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE")      // Allow these methods
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization") // Allow these headers
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		// If preflight request, respond with 200 OK
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Continue processing the request
		next.ServeHTTP(w, r)
	})
}
