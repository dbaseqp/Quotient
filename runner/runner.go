package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"time"

	"quotient/engine"
	"quotient/engine/checks"

	"github.com/redis/go-redis/v9"
)

func main() {
	// Redis connection info
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "quotient_redis:6379"
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: os.Getenv("REDIS_PASSWORD"),
	})
	ctx := context.Background()

	log.Println("Runner started, listening for tasks on Redis at:", "reddisAddr", redisAddr)

	for {
		// Block until we get a task from the "tasks" list (no timeout here).
		val, err := rdb.BLPop(ctx, 0, "tasks").Result()
		if err != nil {
			log.Println("Failed to pop task from Redis: ", "err", err)
			continue
		}

		// val[0] = "tasks", val[1] = the JSON payload
		if len(val) < 2 {
			log.Println("Invalid BLPop response:", val)
			return
		}

		raw := val[1]

		log.Printf("len raw: %d", len(raw))

		var task engine.Task
		if err := json.Unmarshal([]byte(raw), &task); err != nil {
			log.Println("Invalid task format:", "err", err)
			return
		}
		log.Printf("[Runner] Received task: TeamID: %d TeamIdentifier: %s ServiceType: %s", task.TeamID, task.TeamIdentifier, task.ServiceType)

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
			return
		}

		// Deserialize the check data into that runner instance
		if err := json.Unmarshal(task.CheckData, runnerInstance); err != nil {
			log.Println("Failed to unmarshal into", "task.ServiceType", task.ServiceType, "err", err)
			return
		}
		log.Printf("[Runner] CheckData: %+v", runnerInstance)

		// Actually run the check
		resultsChan := make(chan checks.Result)
		go runnerInstance.Run(task.TeamID, task.TeamIdentifier, resultsChan)

		// Block until the check is done
		var result checks.Result
		select {
		case result = <-resultsChan:
		case <-time.After(15 * time.Second):
			result = checks.Result{
				TeamID:      task.TeamID,
				ServiceName: task.ServiceName,
				ServiceType: task.ServiceType,
				Status:      false,
				Debug:       "likely check paniced and couldn't timeout properly",
				Error:       "timeout",
			}
		}

		// Marshall the check result
		resultJSON, _ := json.Marshal(result)

		// Push onto "results" list
		if err := rdb.RPush(ctx, "results", resultJSON).Err(); err != nil {
			log.Printf("Failed to push result to Redis: %v", err)
		} else {
			log.Printf("Pushed result for service %s (TeamID=%d) to 'results'", task.ServiceType, task.TeamID)
		}

	}
}
