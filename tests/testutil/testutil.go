package testutil

import (
	"fmt"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"
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
func StartRedis(t *testing.T) *RedisContainer {
	t.Helper()

	host := os.Getenv("REDIS_HOST")
	if host == "" {
		host = "localhost"
	}
	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}

	client := redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%s:%s", host, port),
	})

	return &RedisContainer{Client: client}
}

// PostgresContainer wraps a Postgres connection string for testing
type PostgresContainer struct {
	connString string
}

// ConnectionString returns the Postgres connection string
func (p *PostgresContainer) ConnectionString() string {
	return p.connString
}

// Close is a no-op for Postgres (connection pooling handled by db package)
func (p *PostgresContainer) Close() {}

// StartPostgres creates a Postgres connection for integration tests.
// Uses POSTGRES_* env vars (set by CI), defaults to localhost.
func StartPostgres(t *testing.T) *PostgresContainer {
	t.Helper()

	host := os.Getenv("POSTGRES_HOST")
	if host == "" {
		host = "localhost"
	}
	port := os.Getenv("POSTGRES_PORT")
	if port == "" {
		port = "5432"
	}
	db := os.Getenv("POSTGRES_DB")
	if db == "" {
		db = "quotient_test"
	}
	user := os.Getenv("POSTGRES_USER")
	if user == "" {
		user = "postgres"
	}
	password := os.Getenv("POSTGRES_PASSWORD")
	if password == "" {
		password = "postgres"
	}

	connString := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, db)

	return &PostgresContainer{connString: connString}
}
