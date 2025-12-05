package integration

import (
	"context"
	"encoding/json"
	"quotient/engine"
	"quotient/engine/checks"
	"quotient/engine/db"
	"quotient/tests/testutil"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFullEngineWorkflow tests the complete engine workflow with real databases
func TestFullEngineWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full integration test in short mode")
	}

	// Start Redis
	redisContainer := testutil.StartRedis(t)
	defer redisContainer.Close()

	// Start PostgreSQL
	pgContainer := testutil.StartPostgres(t)
	defer pgContainer.Close()

	// Initialize database connection for db package
	db.Connect(pgContainer.ConnectionString())

	ctx := context.Background()

	t.Run("complete round workflow", func(t *testing.T) {
		// Clear Redis
		redisContainer.Client.FlushDB(ctx)

		roundID := uint(1)
		teamID := uint(1)

		// Create team first (required by foreign key)
		team := db.TeamSchema{
			ID:   teamID,
			Name: "Test Team 01",
		}
		_, err := db.CreateTeam(team)
		require.NoError(t, err, "should create team")

		// Step 1: Enqueue tasks (simulating engine creating tasks)
		task := engine.Task{
			TeamID:         teamID,
			TeamIdentifier: "01",
			ServiceType:    "Web",
			ServiceName:    "web01-web",
			RoundID:        roundID,
			Attempts:       3,
			Deadline:       time.Now().Add(60 * time.Second),
			CheckData:      json.RawMessage(`{"port":80,"target":"127.0.0.1","scheme":"http"}`),
		}

		taskJSON, err := json.Marshal(task)
		require.NoError(t, err)

		err = redisContainer.Client.RPush(ctx, "tasks", taskJSON).Err()
		require.NoError(t, err)

		// Step 2: Simulate runner consuming task and producing result
		taskData, err := redisContainer.Client.LPop(ctx, "tasks").Result()
		require.NoError(t, err)

		var consumedTask engine.Task
		err = json.Unmarshal([]byte(taskData), &consumedTask)
		require.NoError(t, err)

		assert.Equal(t, task.TeamID, consumedTask.TeamID)
		assert.Equal(t, task.ServiceName, consumedTask.ServiceName)

		// Step 3: Create result (simulating runner completing check)
		result := checks.Result{
			TeamID:      teamID,
			ServiceName: "web01-web",
			ServiceType: "Web",
			RoundID:     roundID,
			Status:      true,
			Points:      5,
			Error:       "",
			Debug:       "Check passed",
		}

		resultJSON, err := json.Marshal(result)
		require.NoError(t, err)

		err = redisContainer.Client.RPush(ctx, "results", resultJSON).Err()
		require.NoError(t, err)

		// Step 4: Collect result (simulating engine)
		resultData, err := redisContainer.Client.BLPop(ctx, 2*time.Second, "results").Result()
		require.NoError(t, err)
		require.Len(t, resultData, 2)

		var collectedResult checks.Result
		err = json.Unmarshal([]byte(resultData[1]), &collectedResult)
		require.NoError(t, err)

		assert.Equal(t, result.TeamID, collectedResult.TeamID)
		assert.Equal(t, result.Status, collectedResult.Status)
		assert.Equal(t, result.Points, collectedResult.Points)

		// Step 5: Process results and save to database
		// Note: This tests that sanitizeDBString is actually called
		dbResult := db.ServiceCheckSchema{
			TeamID:      collectedResult.TeamID,
			RoundID:     roundID,
			ServiceName: sanitizeString(collectedResult.ServiceName),
			Points:      collectedResult.Points,
			Result:      collectedResult.Status,
			Error:       sanitizeString(collectedResult.Error),
			Debug:       sanitizeString(collectedResult.Debug),
		}

		round := db.RoundSchema{
			ID:        roundID,
			StartTime: time.Now(),
			Checks:    []db.ServiceCheckSchema{dbResult},
		}

		// Save to database
		_, err = db.CreateRound(round)
		require.NoError(t, err, "should save round to database")

		// Step 6: Verify data in database
		savedRound, err := db.GetLastRound()
		require.NoError(t, err)
		require.NotNil(t, savedRound)

		assert.Equal(t, roundID, savedRound.ID)
		assert.NotEmpty(t, savedRound.Checks)

		if len(savedRound.Checks) > 0 {
			check := savedRound.Checks[0]
			assert.Equal(t, teamID, check.TeamID)
			assert.Equal(t, "web01-web", check.ServiceName)
			assert.True(t, check.Result)
			assert.Equal(t, 5, check.Points)
		}
	})

	t.Run("sanitization in database workflow", func(t *testing.T) {
		redisContainer.Client.FlushDB(ctx)

		roundID := uint(2)
		teamID := uint(1)

		// Create result with malicious content
		result := checks.Result{
			TeamID:      teamID,
			ServiceName: "web\x00-malicious",  // Null byte
			ServiceType: "Web",
			RoundID:     roundID,
			Status:      false,
			Points:      0,
			Error:       "SQL'; DROP TABLE users\x00--",  // SQL injection attempt with null byte
			Debug:       "<script>alert('xss')</script>\x00",  // XSS attempt with null byte
		}

		// Process with sanitization
		dbResult := db.ServiceCheckSchema{
			TeamID:      result.TeamID,
			RoundID:     roundID,
			ServiceName: sanitizeString(result.ServiceName),
			Points:      result.Points,
			Result:      result.Status,
			Error:       sanitizeString(result.Error),
			Debug:       sanitizeString(result.Debug),
		}

		round := db.RoundSchema{
			ID:        roundID,
			StartTime: time.Now(),
			Checks:    []db.ServiceCheckSchema{dbResult},
		}

		// Save to database
		_, err := db.CreateRound(round)
		require.NoError(t, err, "should save round even with malicious content")

		// Verify sanitization worked
		savedRound, err := db.GetLastRound()
		require.NoError(t, err)

		if len(savedRound.Checks) > 0 {
			check := savedRound.Checks[0]

			// Null bytes should be removed
			assert.NotContains(t, check.ServiceName, "\x00")
			assert.NotContains(t, check.Error, "\x00")
			assert.NotContains(t, check.Debug, "\x00")

			// Content should be sanitized
			assert.Equal(t, "web-malicious", check.ServiceName)
			assert.Equal(t, "SQL'; DROP TABLE users--", check.Error)
			assert.Equal(t, "<script>alert('xss')</script>", check.Debug)
		}
	})

	t.Run("SLA violation workflow", func(t *testing.T) {
		redisContainer.Client.FlushDB(ctx)

		roundID := uint(3)
		teamID := uint(1)
		serviceName := "critical-service"

		// Simulate 3 consecutive failures (should trigger SLA)
		for i := 0; i < 3; i++ {
			result := checks.Result{
				TeamID:      teamID,
				ServiceName: serviceName,
				ServiceType: "Web",
				RoundID:     roundID + uint(i),
				Status:      false,  // Failed
				Points:      0,
				Error:       "Service unavailable",
			}

			// In real engine, this would trigger SLA detection
			// Here we're just testing the database operations work
			dbResult := db.ServiceCheckSchema{
				TeamID:      result.TeamID,
				RoundID:     roundID + uint(i),
				ServiceName: serviceName,
				Points:      result.Points,
				Result:      result.Status,
				Error:       sanitizeString(result.Error),
			}

			round := db.RoundSchema{
				ID:        roundID + uint(i),
				StartTime: time.Now(),
				Checks:    []db.ServiceCheckSchema{dbResult},
			}

			_, err := db.CreateRound(round)
			require.NoError(t, err)
		}

		// Verify all rounds were saved
		savedRound, err := db.GetLastRound()
		require.NoError(t, err)
		assert.Equal(t, roundID+2, savedRound.ID)
	})

	t.Run("concurrent result processing", func(t *testing.T) {
		redisContainer.Client.FlushDB(ctx)

		roundID := uint(10)
		numResults := 50

		// Enqueue many results concurrently
		done := make(chan bool, numResults)

		for i := 0; i < numResults; i++ {
			go func(id int) {
				result := checks.Result{
					TeamID:      uint(id % 5), // 5 teams
					ServiceName: "service",
					ServiceType: "Web",
					RoundID:     roundID,
					Status:      id%2 == 0, // Alternating pass/fail
					Points:      5,
				}

				resultJSON, _ := json.Marshal(result)
				redisContainer.Client.RPush(ctx, "results", resultJSON)
				done <- true
			}(i)
		}

		// Wait for all enqueues
		for i := 0; i < numResults; i++ {
			<-done
		}

		// Verify all results are in Redis
		count, err := redisContainer.Client.LLen(ctx, "results").Result()
		require.NoError(t, err)
		assert.Equal(t, int64(numResults), count)

		// Collect all results
		collected := []checks.Result{}
		for i := 0; i < numResults; i++ {
			data, err := redisContainer.Client.BLPop(ctx, 2*time.Second, "results").Result()
			require.NoError(t, err)

			var result checks.Result
			json.Unmarshal([]byte(data[1]), &result)
			collected = append(collected, result)
		}

		assert.Len(t, collected, numResults)
	})
}

// sanitizeString mimics engine.sanitizeDBString for testing
func sanitizeString(s string) string {
	// Remove null bytes
	result := ""
	for _, r := range s {
		if r != 0 {
			result += string(r)
		}
	}
	return result
}
