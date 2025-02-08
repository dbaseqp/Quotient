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

	log.Println("[Runner] Runner started, listening for tasks on Redis at:", "reddisAddr", redisAddr)

	input := make(chan checks.Result)
	output := make(chan []byte)

	go jsonProcessor(input, output)

	for {
		// Block until we get a task from the "tasks" list (no timeout here).
		val, err := rdb.BLPop(ctx, 0, "tasks").Result()
		if err != nil {
			log.Println("[Runner] Failed to pop task from Redis: ", "err", err)
			continue
		}

		// val[0] = "tasks", val[1] = the JSON payload
		if len(val) < 2 {
			log.Println("[Runner] Invalid BLPop response:", val)
			return
		}
		raw := val[1]

		var task engine.Task
		if err := json.Unmarshal([]byte(raw), &task); err != nil {
			log.Println("[Runner] Invalid task format:", "err", err)
			return
		}
		log.Printf("[Runner] Received task: RoundID: %d TeamID: %d TeamIdentifier: %s ServiceType: %s", task.RoundID, task.TeamID, task.TeamIdentifier, task.ServiceType)

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
			log.Printf("[Runner] Unknown service type %s. Skipping.", task.ServiceType)
			return
		}

		// Deserialize the check data into that runner instance
		if err := json.Unmarshal(task.CheckData, runnerInstance); err != nil {
			log.Println("[Runner] Failed to unmarshal into", "task.ServiceType", task.ServiceType, "err", err)
			return
		}
		log.Printf("[Runner] CheckData: %+v", runnerInstance)

		// Actually run the check
		resultsChan := make(chan checks.Result)
		go runnerInstance.Run(task.TeamID, task.TeamIdentifier, task.RoundID, resultsChan)

		// Block until the check is done
		go func() {
			var result checks.Result
			select {
			case result = <-resultsChan:
			case <-time.After(time.Until(task.Deadline)):
				log.Printf("[Runner] Timeout occured: RoundID=%d, TeamID=%d, ServiceType=%s, ServiceName=%s", task.RoundID, task.TeamID, task.ServiceType, task.ServiceName)
				result = checks.Result{
					TeamID:      task.TeamID,
					ServiceName: task.ServiceName,
					ServiceType: task.ServiceType,
					RoundID:     task.RoundID,
					Status:      false,
					Debug:       "likely check panicked and couldn't timeout properly",
					Error:       "timeout",
				}
			}

			// Marshall the check result
			input <- result
			resultJSON := <-output

			// Push onto "results" list
			if err := rdb.RPush(ctx, "results", resultJSON).Err(); err != nil {
				log.Printf("[Runner] Failed to push result to Redis: %v", err)
			} else {
				log.Printf("[Runner] Pushed result for: RoundID=%d, TeamID=%d, ServiceType=%s, ServiceName=%s", task.RoundID, task.TeamID, task.ServiceType, task.ServiceName)
			}
		}()
	}
}

// serialize json to minimize in use memory
func jsonProcessor(input chan checks.Result, output chan []byte) {
	for result := range input {
		resultJSON, _ := json.Marshal(result)
		output <- resultJSON
	}
}
