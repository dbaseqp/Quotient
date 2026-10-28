package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoggingSetsRequestID(t *testing.T) {
	var seen []any
	handler := Logging(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Context().Value("request_id"))
	})

	handler(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	handler(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	first, ok := seen[0].(string)
	assert.True(t, ok, "the handler sees a request ID")
	assert.NotEmpty(t, first)
	assert.NotEqual(t, seen[0], seen[1], "each request gets its own ID")

	// nolint:staticcheck
	ctx := context.WithValue(context.Background(), "request_id", "existing")
	handler(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	assert.Equal(t, "existing", seen[2], "an existing request ID is kept")
}
