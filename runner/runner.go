package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	"quotient/engine/checks"
	"quotient/engine"
)

// Instantiate one object for each known service type.
// The engine’s "ServiceType" field will map to these keys.
var runnerRegistry = map[string]checks.Runner{
}

func main() {
	// Redis connection info
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "quotient_redis:6379"
	}

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	ctx := context.Background()

	log.Println("Runner started, listening for tasks on Redis at:", redisAddr)

	for {
		// Block until we get a task from the "tasks" list (no timeout here).
		val, err := rdb.BRPop(ctx, 0, "tasks").Result()
		if err != nil {
			log.Fatalf("Failed to pop task from Redis: %v", err)
		}
		// val[0] = "tasks", val[1] = the JSON payload
		if len(val) < 2 {
			log.Println("Invalid BRPop response:", val)
			continue
		}

		raw := val[1]
		var task engine.Task
		if err := json.Unmarshal([]byte(raw), &task); err != nil {
			log.Printf("Invalid task format: %v", err)
			continue
		}
		log.Printf("[Runner] Received task: %+v", task)

		// Pick the correct check struct from runnerRegistry
		var runnerInstance checks.Runner
		switch task.ServiceType {
		case "Custom":
			runnerInstance = &checks.Custom{}
		case "Dns":
			runnerInstance = &checks.Dns{}
		case "Ftp":
			runnerInstance = &checks.Ftp{}
		case "Imap":
			runnerInstance = &checks.Imap{}
		case "Ldap":
			runnerInstance = &checks.Ldap{}
		case "Ping":
			runnerInstance = &checks.Ping{}
		case "Pop3":
			runnerInstance = &checks.Pop3{}
		case "Rdp":
			runnerInstance = &checks.Rdp{}
		case "Smb":
			runnerInstance = &checks.Smb{}
		case "Smtp":
			runnerInstance = &checks.Smtp{}
		case "Sql":
			runnerInstance = &checks.Sql{}
		case "Ssh":
			runnerInstance = &checks.Ssh{}
		case "Tcp":
			runnerInstance = &checks.Tcp{}
		case "Vnc":
			runnerInstance = &checks.Vnc{}
		case "Web":
			runnerInstance = &checks.Web{}
		case "WinRM":
			runnerInstance = &checks.WinRM{}
		default:
			log.Printf("Unknown service type %s. Skipping.", task.ServiceType)
			continue
		}

		// Deserialize the check data into that runner instance
		if err := json.Unmarshal(task.CheckData, runnerInstance); err != nil {
			log.Printf("Failed to unmarshal into %s: %v", task.ServiceType, err)
			continue
		}
        log.Printf("[Runner] CheckData: %+v", runnerInstance)

		// Actually run the check
		resultsChan := make(chan checks.Result)
		go runnerInstance.Run(task.TeamID, task.TeamIdentifier, resultsChan)
		var result checks.Result

		select {
		case result = <-resultsChan:
			// success or failure, we got a result
		case <-time.After(30 * time.Second):
			// in case the check never returns
			result.Error = "runner internal timeout"
			result.TeamID = task.TeamID
			result.ServiceType = task.ServiceType
			result.ServiceName = runnerInstance.Service.Name
			result.Status = false
			log.Printf("Runner internal timeout for service type: %s", task.ServiceType)
		}

		// Marshall the check result
		resultJSON, _ := json.Marshal(result)

		// Push onto "results" list
		if err := rdb.LPush(ctx, "results", resultJSON).Err(); err != nil {
			log.Printf("Failed to push result to Redis: %v", err)
		} else {
			log.Printf("Pushed result for service %s (TeamID=%d) to 'results'", task.ServiceType, task.TeamID)
		}
	}
}
