package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dbaseqp/Quotient/www/api"

	"github.com/stretchr/testify/assert"
)

func TestLoggingSetsRequestID(t *testing.T) {
	var seen []string
	handler := Logging(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, api.RequestID(r.Context()))
	})

	handler(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	handler(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Len(t, seen[0], 8, "the handler sees a request ID")
	assert.NotEqual(t, seen[0], seen[1], "each request gets its own ID")

	ctx := api.WithRequestID(context.Background(), "existing")
	handler(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	assert.Equal(t, "existing", seen[2], "an existing request ID is kept")
}
