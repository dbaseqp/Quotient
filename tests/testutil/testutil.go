package testutil

import (
	"cmp"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// RedisContainer wraps a Redis client for testing
type RedisContainer struct {
	Client *redis.Client
}

// Close closes the Redis client
func (r *RedisContainer) Close() error {
	if r.Client != nil {
		return r.Client.Close()
	}
	return nil
}

// StartRedis creates a Redis client for integration tests.
// Uses REDIS_HOST and REDIS_PORT env vars (set by CI), defaults to localhost:6379.
func startRedis(t *testing.T) *RedisContainer {
	t.Helper()

	host := cmp.Or(os.Getenv("REDIS_HOST"), "localhost")
	port := cmp.Or(os.Getenv("REDIS_PORT"), "6380")
	password := cmp.Or(os.Getenv("REDIS_PASSWORD"), "redis_password")

	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", host, port),
		Password: password,
	})

	return &RedisContainer{Client: client}
}

// PostgresContainer wraps a Postgres connection string for testing
type PostgresContainer struct {
	DB *db.DB
}

func (p *PostgresContainer) Close() error {
	return p.DB.Close()
}

// StartPostgres creates a Postgres connection for integration tests.
// Uses POSTGRES_* env vars (set by CI), defaults to localhost.
func startPostgres(t *testing.T) *PostgresContainer {
	t.Helper()

	host := cmp.Or(os.Getenv("POSTGRES_HOST"), "localhost")
	port := cmp.Or(os.Getenv("POSTGRES_PORT"), "5432")
	dbname := cmp.Or(os.Getenv("POSTGRES_DB"), "engine")
	user := cmp.Or(os.Getenv("POSTGRES_USER"), "engineuser")
	password := cmp.Or(os.Getenv("POSTGRES_PASSWORD"), "postgres_password")

	connString := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	return &PostgresContainer{DB: db.Connect(connString)}
}

// testTeamCounter provides unique team IDs across test runs
var testTeamCounter atomic.Uint64

func init() {
	testTeamCounter.Store(uint64(time.Now().UnixNano() % 1_000_000))
}

// nextTeamID returns a unique team ID for testing
func nextTeamID() uint {
	return uint(testTeamCounter.Add(1))
}

// createTestTeam creates a team with a unique ID, or returns existing if name matches
func (p *PostgresContainer) CreateTestTeam(t *testing.T, name string, identifier string) db.TeamSchema {
	t.Helper()
	teamID := nextTeamID()
	team := db.TeamSchema{
		ID:         teamID,
		Name:       fmt.Sprintf("%s-%d", name, teamID),
		Identifier: identifier,
		Active:     true,
	}
	_, err := p.DB.CreateTeam(team)
	require.NoError(t, err)
	return team
}

func StartContainers(t *testing.T) (*RedisContainer, *PostgresContainer) {
	t.Helper()
	redis := startRedis(t)
	pg := startPostgres(t)

	t.Cleanup(func() {
		require.NoError(t, pg.Close())
		require.NoError(t, redis.Close())
	})

	return redis, pg
}
