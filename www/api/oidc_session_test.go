package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine"
	"github.com/dbaseqp/Quotient/engine/config"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRedis answers GET from sessions, or fails every command while down.
// Commands never reach the network.
type fakeRedis struct {
	down     bool
	sessions map[string]string
}

func (f *fakeRedis) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, errors.New("fakeRedis: no network")
	}
}

func (f *fakeRedis) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if f.down {
			err := errors.New("dial tcp: connect: connection refused")
			cmd.SetErr(err)
			return err
		}
		get, ok := cmd.(*redis.StringCmd)
		if !ok || cmd.Name() != "get" {
			return nil
		}
		value, found := f.sessions[cmd.Args()[1].(string)]
		if !found {
			get.SetErr(redis.Nil)
			return redis.Nil
		}
		get.SetVal(value)
		return nil
	}
}

func (f *fakeRedis) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// authenticateOIDC runs Authenticate with an OIDC session cookie for username
// and reports whether it succeeded and whether it cleared the cookie.
func authenticateOIDC(t *testing.T, a *API, username string) (ok, cleared bool) {
	t.Helper()
	value, err := CookieEncoder.Encode(COOKIENAME, map[string]any{"username": username, "authSource": "oidc"})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodGet, "/api/injects", nil)
	req.AddCookie(&http.Cookie{Name: COOKIENAME, Value: value})
	rec := httptest.NewRecorder()
	_, ok = a.Authenticate(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == COOKIENAME && (c.MaxAge < 0 || c.Value == "") {
			cleared = true
		}
	}
	return ok, cleared
}

// A Redis outage is not an expired session: it must not log OIDC users out,
// and their session must work again once Redis is back. A session that is
// really gone still clears the cookie.
func TestRedisErrorKeepsOIDCSession(t *testing.T) {
	session, err := json.Marshal(OidcUserInfo{Username: "alice", Roles: []string{"admin"}, ExpiresAt: time.Now().Add(time.Hour)})
	require.NoError(t, err)
	fake := &fakeRedis{down: true, sessions: map[string]string{"oidc:session:alice": string(session)}}
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	rdb.AddHook(fake)
	t.Cleanup(func() { _ = rdb.Close() })
	a := NewAPI(config.NewLive(&config.ConfigSettings{}), &engine.ScoringEngine{RedisClient: rdb})

	ok, cleared := authenticateOIDC(t, a, "alice")
	assert.False(t, ok, "identity cannot be resolved while Redis is down")
	assert.False(t, cleared, "Redis outage cleared the OIDC session cookie (user logged out)")

	fake.down = false
	ok, _ = authenticateOIDC(t, a, "alice")
	assert.True(t, ok, "session works again once Redis is back")

	ok, cleared = authenticateOIDC(t, a, "bob")
	assert.False(t, ok)
	assert.True(t, cleared, "a missing OIDC session still clears the cookie")
}
