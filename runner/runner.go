package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"quotient/engine"
	"quotient/engine/checks"

	"github.com/redis/go-redis/v9"
	reaper "github.com/ramr/go-reaper"
)

// Global variable to store the runner ID
var runnerID string

func main() {
	// Use WithReaper to run reaper as PID 1 and application code in a child process
	// This prevents the reaper from interfering with processes we're actively managing
	reaper.WithReaper(reaper.Config{}, runApp)
}

func runApp(err error) int {
	if err != nil {
		log.Printf("[Runner] Error from reaper: %v", err)
		return 1
	}

	// Use environment variable RUNNER_ID if set, otherwise use hostname
	runnerID = os.Getenv("RUNNER_ID")
	if runnerID == "" {
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "unknown"
		}
		runnerID = hostname
	}

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "quotient_redis:6379"
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: os.Getenv("REDIS_PASSWORD"),
	})
	ctx := context.Background()

	log.Printf("[Runner] Started with ID %s, listening for tasks on Redis at: %s", runnerID, redisAddr)

	go func() {
		events := rdb.Subscribe(context.Background(), "events")
		defer events.Close()
		eventsChannel := events.Channel()

		for msg := range eventsChannel {
			log.Printf("[Runner] Received message: %v", msg)
			if msg.Payload == "reset" {
				log.Printf("[Runner] Reset event received, quitting...")
				os.Exit(0)
			} else {
				continue
			}
		}
	}()

	for {
		task, err := getNextTask(ctx, rdb)
		if err != nil {
			log.Printf("[Runner] Error getting task: %v", err)
			continue
		}

		runner, err := createRunner(task)
		if err != nil {
			log.Printf("[Runner] Error creating runner: %v", err)
			continue
		}

		go handleTask(ctx, rdb, runner, task)
	}
}

func getNextTask(ctx context.Context, rdb *redis.Client) (*engine.Task, error) {
	// Block until we get a task from the "tasks" list
	val, err := rdb.BLPop(ctx, 0, "tasks").Result()
	if err != nil {
		return nil, fmt.Errorf("failed to pop task: %w", err)
	}

	// val[0] = "tasks", val[1] = the JSON payload
	if len(val) < 2 {
		return nil, fmt.Errorf("invalid BLPop response: %v", val)
	}

	var task engine.Task
	if err := json.Unmarshal([]byte(val[1]), &task); err != nil {
		return nil, fmt.Errorf("invalid task format: %w", err)
	}

	log.Printf("[Runner] Received task: RoundID=%d TeamID=%d TeamIdentifier=%s ServiceType=%s",
		task.RoundID, task.TeamID, task.TeamIdentifier, task.ServiceType)

	return &task, nil
}

func createRunner(task *engine.Task) (checks.Runner, error) {
	var runner checks.Runner

	switch task.ServiceType {
	case "Custom":
		runner = &checks.Custom{}
	case "Dns":
		runner = &checks.Dns{}
	case "Ftp":
		runner = &checks.Ftp{}
	case "Imap":
		runner = &checks.Imap{}
	case "Ldap":
		runner = &checks.Ldap{}
	case "Ping":
		runner = &checks.Ping{}
	case "Pop3":
		runner = &checks.Pop3{}
	case "Rdp":
		runner = &checks.Rdp{}
	case "Smb":
		runner = &checks.Smb{}
	case "Smtp":
		runner = &checks.Smtp{}
	case "Sql":
		runner = &checks.Sql{}
	case "Ssh":
		runner = &checks.Ssh{}
	case "Tcp":
		runner = &checks.Tcp{}
	case "Vnc":
		runner = &checks.Vnc{}
	case "Web":
		runner = &checks.Web{}
	case "WinRM":
		runner = &checks.WinRM{}
	default:
		return nil, fmt.Errorf("unknown service type: %s", task.ServiceType)
	}

	if err := json.Unmarshal(task.CheckData, runner); err != nil {
		return nil, fmt.Errorf("failed to unmarshal check data: %w", err)
	}

	log.Printf("[Runner] CheckData: %+v", runner)
	return runner, nil
}

func handleTask(ctx context.Context, rdb *redis.Client, runner checks.Runner, task *engine.Task) {
	// Create a task key to identify the check
	taskKey := fmt.Sprintf("task:%d:%d:%s:%s", task.RoundID, task.TeamID, task.ServiceType, task.ServiceName)

	// Create a result
	result := checks.Result{
		TeamID:      task.TeamID,
		ServiceName: task.ServiceName,
		ServiceType: task.ServiceType,
		RoundID:     task.RoundID,
		Status:      false,
		RunnerID:    runnerID,
		StartTime:   time.Now().Format(time.RFC3339),
		StatusText:  "running",
	}

	// Set initial "running" status in Redis for visualization
	statusJSON, _ := json.Marshal(result)
	rdb.Set(ctx, taskKey, statusJSON, time.Until(task.Deadline))

	resultsChan := make(chan checks.Result, 1)

	// this currently discards all failed attempts
	for i := range task.Attempts {
		log.Printf("[Runner] Running check: RoundID=%d TeamID=%d ServiceType=%s ServiceName=%s Attempt=%d",
			task.RoundID, task.TeamID, task.ServiceType, task.ServiceName, i+1)

		// Create context with deadline
		checkCtx, cancel := context.WithDeadline(ctx, task.Deadline)
		defer cancel()

		// Run the check in a goroutine
		go runner.Run(task.TeamID, task.TeamIdentifier, task.RoundID, resultsChan)

		// Wait for either result or deadline
		select {
		case result = <-resultsChan:
			result.TeamID = task.TeamID
			result.ServiceName = task.ServiceName
			result.ServiceType = task.ServiceType
			result.RoundID = task.RoundID

			log.Printf("[Runner] Check result received: RoundID=%d TeamID=%d ServiceType=%s Status=%v Debug=%s Error=%s",
				result.RoundID, result.TeamID, result.ServiceType, result.Status, result.Debug, result.Error)

		case <-checkCtx.Done():
			result.Status = false
			result.Debug = "round ended before check completed"
			result.Error = "timeout"
			result.TeamID = task.TeamID
			result.ServiceName = task.ServiceName
			result.ServiceType = task.ServiceType
			result.RoundID = task.RoundID

			log.Printf("[Runner] Check timed out: RoundID=%d TeamID=%d ServiceType=%s",
				task.RoundID, task.TeamID, task.ServiceType)
		}

		// Break if successful or deadline passed
		if result.Status || time.Now().After(task.Deadline) {
			break
		}
	}

	// Marshal and store result
	resultJSON, err := json.Marshal(result)
	if err != nil {
		log.Printf("[Runner] Failed to marshal result: %v", err)
		return
	}

	if err := rdb.RPush(ctx, "results", resultJSON).Err(); err != nil {
		log.Printf("[Runner] Failed to push result to Redis: %v", err)
		return
	}

	log.Printf("[Runner] Successfully pushed result: RoundID=%d TeamID=%d ServiceType=%s Status=%v",
		result.RoundID, result.TeamID, result.ServiceType, result.Status)

	// Update the task key with the final result status
	result.EndTime = time.Now().Format(time.RFC3339)
	result.StatusText = map[bool]string{true: "success", false: "failed"}[result.Status]
	statusJSON, _ = json.Marshal(result)
	// Use a longer TTL for completed tasks to ensure they're visible in the UI
	rdb.Set(ctx, taskKey, statusJSON, 3*time.Minute)
}
