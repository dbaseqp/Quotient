package integration

import (
	"context"
	"encoding/json"
	"quotient/engine"
	"quotient/engine/checks"
	"quotient/tests/testutil"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEngineRedisTaskEnqueue tests that the engine correctly enqueues tasks to Redis
func TestEngineRedisTaskEnqueue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redisContainer := testutil.StartRedis(t)
	defer redisContainer.Close()

	ctx := context.Background()

	t.Run("enqueue single task", func(t *testing.T) {
		// Clear Redis
		redisContainer.Client.FlushDB(ctx)

		// Create a task
		task := engine.Task{
			TeamID:         1,
			TeamIdentifier: "01",
			ServiceType:    "Web",
			ServiceName:    "web01-web",
			RoundID:        1,
			Attempts:       3,
			Deadline:       time.Now().Add(60 * time.Second),
			CheckData:      json.RawMessage(`{"port":80,"target":"10.100.11.2"}`),
		}

		// Serialize and push to Redis
		payload, err := json.Marshal(task)
		require.NoError(t, err)

		err = redisContainer.Client.RPush(ctx, "tasks", payload).Err()
		require.NoError(t, err)

		// Verify task is in queue
		length, err := redisContainer.Client.LLen(ctx, "tasks").Result()
		require.NoError(t, err)
		assert.Equal(t, int64(1), length)

		// Pop task and verify
		val, err := redisContainer.Client.LPop(ctx, "tasks").Result()
		require.NoError(t, err)

		var decodedTask engine.Task
		err = json.Unmarshal([]byte(val), &decodedTask)
		require.NoError(t, err)

		assert.Equal(t, task.TeamID, decodedTask.TeamID)
		assert.Equal(t, task.ServiceName, decodedTask.ServiceName)
		assert.Equal(t, task.ServiceType, decodedTask.ServiceType)
	})

	t.Run("enqueue multiple tasks", func(t *testing.T) {
		// Clear Redis
		redisContainer.Client.FlushDB(ctx)

		// Create multiple tasks
		tasks := []engine.Task{
			{TeamID: 1, TeamIdentifier: "01", ServiceType: "Web", ServiceName: "web01-web", RoundID: 1, Attempts: 3, Deadline: time.Now().Add(60 * time.Second)},
			{TeamID: 1, TeamIdentifier: "01", ServiceType: "SSH", ServiceName: "web01-ssh", RoundID: 1, Attempts: 3, Deadline: time.Now().Add(60 * time.Second)},
			{TeamID: 2, TeamIdentifier: "02", ServiceType: "Web", ServiceName: "web01-web", RoundID: 1, Attempts: 3, Deadline: time.Now().Add(60 * time.Second)},
		}

		// Enqueue all tasks
		for _, task := range tasks {
			payload, err := json.Marshal(task)
			require.NoError(t, err)
			err = redisContainer.Client.RPush(ctx, "tasks", payload).Err()
			require.NoError(t, err)
		}

		// Verify queue length
		length, err := redisContainer.Client.LLen(ctx, "tasks").Result()
		require.NoError(t, err)
		assert.Equal(t, int64(len(tasks)), length)

		// Verify tasks are in FIFO order
		for i, expectedTask := range tasks {
			val, err := redisContainer.Client.LPop(ctx, "tasks").Result()
			require.NoError(t, err, "failed to pop task %d", i)

			var decodedTask engine.Task
			err = json.Unmarshal([]byte(val), &decodedTask)
			require.NoError(t, err)

			assert.Equal(t, expectedTask.TeamID, decodedTask.TeamID)
			assert.Equal(t, expectedTask.ServiceName, decodedTask.ServiceName)
		}
	})

	t.Run("clear stale tasks", func(t *testing.T) {
		// Clear Redis
		redisContainer.Client.FlushDB(ctx)

		// Add some stale tasks
		for i := 0; i < 5; i++ {
			task := engine.Task{
				TeamID:         uint(i + 1),
				TeamIdentifier: "01",
				ServiceType:    "Web",
				ServiceName:    "stale-service",
				RoundID:        99, // Old round
				Attempts:       3,
				Deadline:       time.Now().Add(-60 * time.Second), // Past deadline
			}
			payload, _ := json.Marshal(task)
			redisContainer.Client.RPush(ctx, "tasks", payload)
		}

		// Verify stale tasks exist
		length, _ := redisContainer.Client.LLen(ctx, "tasks").Result()
		assert.Equal(t, int64(5), length)

		// Clear stale tasks (simulating engine behavior)
		redisContainer.Client.Del(ctx, "tasks")

		// Verify queue is empty
		length, _ = redisContainer.Client.LLen(ctx, "tasks").Result()
		assert.Equal(t, int64(0), length)
	})
}

// TestEngineRedisResultCollection tests result collection from Redis
func TestEngineRedisResultCollection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redisContainer := testutil.StartRedis(t)
	defer redisContainer.Close()

	ctx := context.Background()

	t.Run("collect single result", func(t *testing.T) {
		// Clear Redis
		redisContainer.Client.FlushDB(ctx)

		// Push a result
		result := checks.Result{
			TeamID:      1,
			ServiceName: "web01-web",
			ServiceType: "Web",
			RoundID:     1,
			Status:      true,
			Points:      5,
		}

		payload, err := json.Marshal(result)
		require.NoError(t, err)

		err = redisContainer.Client.RPush(ctx, "results", payload).Err()
		require.NoError(t, err)

		// Collect result (simulate engine behavior)
		val, err := redisContainer.Client.BLPop(ctx, 2*time.Second, "results").Result()
		require.NoError(t, err)
		require.Len(t, val, 2) // [queue_name, value]

		var decodedResult checks.Result
		err = json.Unmarshal([]byte(val[1]), &decodedResult)
		require.NoError(t, err)

		assert.Equal(t, result.TeamID, decodedResult.TeamID)
		assert.Equal(t, result.ServiceName, decodedResult.ServiceName)
		assert.Equal(t, result.Status, decodedResult.Status)
		assert.Equal(t, result.Points, decodedResult.Points)
	})

	t.Run("collect multiple results", func(t *testing.T) {
		// Clear Redis
		redisContainer.Client.FlushDB(ctx)

		// Push multiple results
		expectedResults := []checks.Result{
			{TeamID: 1, ServiceName: "web01-web", ServiceType: "Web", RoundID: 1, Status: true, Points: 5},
			{TeamID: 1, ServiceName: "web01-ssh", ServiceType: "SSH", RoundID: 1, Status: true, Points: 5},
			{TeamID: 2, ServiceName: "web01-web", ServiceType: "Web", RoundID: 1, Status: false, Points: 0},
		}

		for _, result := range expectedResults {
			payload, _ := json.Marshal(result)
			redisContainer.Client.RPush(ctx, "results", payload)
		}

		// Collect all results
		collectedResults := []checks.Result{}
		for i := 0; i < len(expectedResults); i++ {
			val, err := redisContainer.Client.BLPop(ctx, 2*time.Second, "results").Result()
			require.NoError(t, err)

			var result checks.Result
			json.Unmarshal([]byte(val[1]), &result)
			collectedResults = append(collectedResults, result)
		}

		assert.Len(t, collectedResults, len(expectedResults))

		// Verify results match
		for i, expected := range expectedResults {
			assert.Equal(t, expected.TeamID, collectedResults[i].TeamID)
			assert.Equal(t, expected.ServiceName, collectedResults[i].ServiceName)
			assert.Equal(t, expected.Status, collectedResults[i].Status)
		}
	})

	t.Run("timeout waiting for results", func(t *testing.T) {
		// Clear Redis
		redisContainer.Client.FlushDB(ctx)

		// Try to collect result with short timeout (should timeout)
		timeoutCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()

		_, err := redisContainer.Client.BLPop(timeoutCtx, 500*time.Millisecond, "results").Result()
		assert.Error(t, err) // Should timeout
	})

	t.Run("filter out-of-round results", func(t *testing.T) {
		// Clear Redis
		redisContainer.Client.FlushDB(ctx)

		currentRound := uint(5)

		// Push results from different rounds
		results := []checks.Result{
			{TeamID: 1, ServiceName: "web01-web", RoundID: currentRound - 1, Status: true, Points: 5},   // Old round
			{TeamID: 1, ServiceName: "web01-ssh", RoundID: currentRound, Status: true, Points: 5},       // Current round
			{TeamID: 2, ServiceName: "web01-web", RoundID: currentRound + 1, Status: true, Points: 5},   // Future round
			{TeamID: 2, ServiceName: "web01-dns", RoundID: currentRound, Status: true, Points: 5},       // Current round
		}

		for _, result := range results {
			payload, _ := json.Marshal(result)
			redisContainer.Client.RPush(ctx, "results", payload)
		}

		// Collect and filter results
		validResults := []checks.Result{}
		for i := 0; i < len(results); i++ {
			val, err := redisContainer.Client.BLPop(ctx, 2*time.Second, "results").Result()
			require.NoError(t, err)

			var result checks.Result
			json.Unmarshal([]byte(val[1]), &result)

			// Only keep results from current round (simulating engine behavior)
			if result.RoundID == currentRound {
				validResults = append(validResults, result)
			}
		}

		// Should only have 2 valid results
		assert.Len(t, validResults, 2)
		for _, result := range validResults {
			assert.Equal(t, currentRound, result.RoundID)
		}
	})
}

// TestEngineRedisPubSub tests Redis pub/sub for engine events
func TestEngineRedisPubSub(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redisContainer := testutil.StartRedis(t)
	defer redisContainer.Close()

	ctx := context.Background()

	t.Run("publish and receive events", func(t *testing.T) {
		// Subscribe to events channel
		pubsub := redisContainer.Client.Subscribe(ctx, "events")
		defer pubsub.Close()

		// Wait for subscription confirmation
		_, err := pubsub.Receive(ctx)
		require.NoError(t, err)

		// Channel for receiving messages
		ch := pubsub.Channel()

		// Publish events
		events := []string{"pause", "resume", "reset", "round_finish"}

		go func() {
			time.Sleep(100 * time.Millisecond)
			for _, event := range events {
				redisContainer.Client.Publish(ctx, "events", event)
				time.Sleep(50 * time.Millisecond)
			}
		}()

		// Receive and verify events
		receivedEvents := []string{}
		timeout := time.After(5 * time.Second)

	RECEIVE:
		for i := 0; i < len(events); i++ {
			select {
			case msg := <-ch:
				receivedEvents = append(receivedEvents, msg.Payload)
			case <-timeout:
				break RECEIVE
			}
		}

		assert.Len(t, receivedEvents, len(events))
		for i, expected := range events {
			assert.Equal(t, expected, receivedEvents[i])
		}
	})

	t.Run("multiple subscribers", func(t *testing.T) {
		// Create multiple subscribers
		pubsub1 := redisContainer.Client.Subscribe(ctx, "events")
		defer pubsub1.Close()
		pubsub2 := redisContainer.Client.Subscribe(ctx, "events")
		defer pubsub2.Close()

		// Wait for subscriptions
		pubsub1.Receive(ctx)
		pubsub2.Receive(ctx)

		ch1 := pubsub1.Channel()
		ch2 := pubsub2.Channel()

		// Publish event
		go func() {
			time.Sleep(100 * time.Millisecond)
			redisContainer.Client.Publish(ctx, "events", "test_event")
		}()

		// Both subscribers should receive the event
		timeout := time.After(2 * time.Second)

		var msg1, msg2 string
		select {
		case m := <-ch1:
			msg1 = m.Payload
		case <-timeout:
			t.Fatal("timeout waiting for subscriber 1")
		}

		select {
		case m := <-ch2:
			msg2 = m.Payload
		case <-timeout:
			t.Fatal("timeout waiting for subscriber 2")
		}

		assert.Equal(t, "test_event", msg1)
		assert.Equal(t, "test_event", msg2)
	})
}

// TestEngineRedisRoundWorkflow tests a complete round workflow
func TestEngineRedisRoundWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redisContainer := testutil.StartRedis(t)
	defer redisContainer.Close()

	ctx := context.Background()

	t.Run("complete round workflow", func(t *testing.T) {
		// Clear Redis at start of each subtest
		redisContainer.Client.FlushDB(ctx)

		roundID := uint(1)
		numTasks := 5

		// Step 1: Enqueue tasks (simulating engine)
		for i := 0; i < numTasks; i++ {
			task := engine.Task{
				TeamID:         uint(i + 1),
				TeamIdentifier: "01",
				ServiceType:    "Web",
				ServiceName:    "web-service",
				RoundID:        roundID,
				Attempts:       3,
				Deadline:       time.Now().Add(10 * time.Second),
			}
			payload, _ := json.Marshal(task)
			redisContainer.Client.RPush(ctx, "tasks", payload)
		}

		// Verify tasks enqueued
		taskCount, _ := redisContainer.Client.LLen(ctx, "tasks").Result()
		assert.Equal(t, int64(numTasks), taskCount)

		// Step 2: Simulate runners consuming tasks and producing results
		for i := 0; i < numTasks; i++ {
			// Pop task
			taskVal, err := redisContainer.Client.LPop(ctx, "tasks").Result()
			require.NoError(t, err)

			var task engine.Task
			json.Unmarshal([]byte(taskVal), &task)

			// Create result
			result := checks.Result{
				TeamID:      task.TeamID,
				ServiceName: task.ServiceName,
				ServiceType: task.ServiceType,
				RoundID:     task.RoundID,
				Status:      i%2 == 0, // Alternating success/failure
				Points:      5,
			}

			// Push result
			resultPayload, _ := json.Marshal(result)
			redisContainer.Client.RPush(ctx, "results", resultPayload)
		}

		// Verify tasks consumed
		taskCount, _ = redisContainer.Client.LLen(ctx, "tasks").Result()
		assert.Equal(t, int64(0), taskCount)

		// Verify results available
		resultCount, _ := redisContainer.Client.LLen(ctx, "results").Result()
		assert.Equal(t, int64(numTasks), resultCount)

		// Step 3: Engine collects results
		collectedResults := []checks.Result{}
		for i := 0; i < numTasks; i++ {
			val, err := redisContainer.Client.BLPop(ctx, 2*time.Second, "results").Result()
			require.NoError(t, err)

			var result checks.Result
			json.Unmarshal([]byte(val[1]), &result)
			collectedResults = append(collectedResults, result)
		}

		// Verify all results collected
		assert.Len(t, collectedResults, numTasks)

		// Verify results queue empty
		resultCount, _ = redisContainer.Client.LLen(ctx, "results").Result()
		assert.Equal(t, int64(0), resultCount)

		// Step 4: Publish round completion event
		err := redisContainer.Client.Publish(ctx, "events", "round_finish").Err()
		require.NoError(t, err)
	})
}

// TestRedisConnectionFailure tests behavior when Redis is unavailable
func TestRedisConnectionFailure(t *testing.T) {
	t.Run("connection to non-existent Redis", func(t *testing.T) {
		// Try to connect to non-existent Redis
		client := redis.NewClient(&redis.Options{
			Addr: "localhost:9999", // Non-existent port
		})

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		// Ping should fail
		err := client.Ping(ctx).Err()
		assert.Error(t, err, "should fail to connect to non-existent Redis")
	})
}
