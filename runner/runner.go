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
		Addr: redisAddr,
		Password: os.Getenv("REDIS_PASSWORD"),
	})
	ctx := context.Background()

	log.Println("Runner started, listening for tasks on Redis at:", redisAddr)

	for {
		// Block until we get a task from the "tasks" list (no timeout here).
		val, err := rdb.BLPop(ctx, 0, "tasks").Result()
		if err != nil {
			log.Fatalf("Failed to pop task from Redis: %v", err)
		}
		// val[0] = "tasks", val[1] = the JSON payload
		if len(val) < 2 {
			log.Println("Invalid BLPop response:", val)
			continue
		}

		raw := val[1]
		var task engine.Task
		if err := json.Unmarshal([]byte(raw), &task); err != nil {
			log.Printf("Invalid task format: %v", err)
			continue
		}
		log.Printf("[Runner] Received task: TeamID: %d TeamIdentifier: %s ServiceType: %s", task.TeamID, task.TeamIdentifier, task.ServiceType)

		// Pick the correct check struct from runnerRegistry
		var runnerInstance checks.Runner
		var serviceName string
		switch task.ServiceType {
		case "Custom":
			runnerInstance = &checks.Custom{}
			// Deserialize the check data into that runner instance
			if err := json.Unmarshal(task.CheckData, runnerInstance); err != nil {
				log.Printf("Failed to unmarshal into %s: %v", task.ServiceType, err)
				continue
			}
			log.Printf("[Runner] CheckData: %+v", runnerInstance)
			serviceName = runnerInstance.(*checks.Custom).Service.Name
		case "Dns":
			runnerInstance = &checks.Dns{}
			// Deserialize the check data into that runner instance
			if err := json.Unmarshal(task.CheckData, runnerInstance); err != nil {
				log.Printf("Failed to unmarshal into %s: %v", task.ServiceType, err)
				continue
			}
			log.Printf("[Runner] CheckData: %+v", runnerInstance)
			serviceName = runnerInstance.(*checks.Dns).Service.Name
		case "Ftp":
			runnerInstance = &checks.Ftp{}
			// Deserialize the check data into that runner instance
			if err := json.Unmarshal(task.CheckData, runnerInstance); err != nil {
				log.Printf("Failed to unmarshal into %s: %v", task.ServiceType, err)
				continue
			}
			log.Printf("[Runner] CheckData: %+v", runnerInstance)
			serviceName = runnerInstance.(*checks.Ftp).Service.Name
		case "Imap":
			runnerInstance = &checks.Imap{}
			// Deserialize the check data into that runner instance
			if err := json.Unmarshal(task.CheckData, runnerInstance); err != nil {
				log.Printf("Failed to unmarshal into %s: %v", task.ServiceType, err)
				continue
			}
			log.Printf("[Runner] CheckData: %+v", runnerInstance)
			serviceName = runnerInstance.(*checks.Imap).Service.Name
		case "Ldap":
			runnerInstance = &checks.Ldap{}
			// Deserialize the check data into that runner instance
			if err := json.Unmarshal(task.CheckData, runnerInstance); err != nil {
				log.Printf("Failed to unmarshal into %s: %v", task.ServiceType, err)
				continue
			}
			log.Printf("[Runner] CheckData: %+v", runnerInstance)
			serviceName = runnerInstance.(*checks.Ldap).Service.Name
		case "Ping":
			runnerInstance = &checks.Ping{}
			// Deserialize the check data into that runner instance
			if err := json.Unmarshal(task.CheckData, runnerInstance); err != nil {
				log.Printf("Failed to unmarshal into %s: %v", task.ServiceType, err)
				continue
			}
			log.Printf("[Runner] CheckData: %+v", runnerInstance)
			serviceName = runnerInstance.(*checks.Ping).Service.Name
		case "Pop3":
			runnerInstance = &checks.Pop3{}
			// Deserialize the check data into that runner instance
			if err := json.Unmarshal(task.CheckData, runnerInstance); err != nil {
				log.Printf("Failed to unmarshal into %s: %v", task.ServiceType, err)
				continue
			}
			log.Printf("[Runner] CheckData: %+v", runnerInstance)
			serviceName = runnerInstance.(*checks.Pop3).Service.Name
		case "Rdp":
			runnerInstance = &checks.Rdp{}
			// Deserialize the check data into that runner instance
			if err := json.Unmarshal(task.CheckData, runnerInstance); err != nil {
				log.Printf("Failed to unmarshal into %s: %v", task.ServiceType, err)
				continue
			}
			log.Printf("[Runner] CheckData: %+v", runnerInstance)
			serviceName = runnerInstance.(*checks.Rdp).Service.Name
		case "Smb":
			runnerInstance = &checks.Smb{}
			// Deserialize the check data into that runner instance
			if err := json.Unmarshal(task.CheckData, runnerInstance); err != nil {
				log.Printf("Failed to unmarshal into %s: %v", task.ServiceType, err)
				continue
			}
			log.Printf("[Runner] CheckData: %+v", runnerInstance)
			serviceName = runnerInstance.(*checks.Smb).Service.Name
		case "Smtp":
			runnerInstance = &checks.Smtp{}
			// Deserialize the check data into that runner instance
			if err := json.Unmarshal(task.CheckData, runnerInstance); err != nil {
				log.Printf("Failed to unmarshal into %s: %v", task.ServiceType, err)
				continue
			}
			log.Printf("[Runner] CheckData: %+v", runnerInstance)
			serviceName = runnerInstance.(*checks.Smtp).Service.Name
		case "Sql":
			runnerInstance = &checks.Sql{}
			// Deserialize the check data into that runner instance
			if err := json.Unmarshal(task.CheckData, runnerInstance); err != nil {
				log.Printf("Failed to unmarshal into %s: %v", task.ServiceType, err)
				continue
			}
			log.Printf("[Runner] CheckData: %+v", runnerInstance)
			serviceName = runnerInstance.(*checks.Sql).Service.Name
		case "Ssh":
			runnerInstance = &checks.Ssh{}
			// Deserialize the check data into that runner instance
			if err := json.Unmarshal(task.CheckData, runnerInstance); err != nil {
				log.Printf("Failed to unmarshal into %s: %v", task.ServiceType, err)
				continue
			}
			log.Printf("[Runner] CheckData: %+v", runnerInstance)
			serviceName = runnerInstance.(*checks.Ssh).Service.Name
		case "Tcp":
			runnerInstance = &checks.Tcp{}
			// Deserialize the check data into that runner instance
			if err := json.Unmarshal(task.CheckData, runnerInstance); err != nil {
				log.Printf("Failed to unmarshal into %s: %v", task.ServiceType, err)
				continue
			}
			log.Printf("[Runner] CheckData: %+v", runnerInstance)
			serviceName = runnerInstance.(*checks.Tcp).Service.Name
		case "Vnc":
			runnerInstance = &checks.Vnc{}
			// Deserialize the check data into that runner instance
			if err := json.Unmarshal(task.CheckData, runnerInstance); err != nil {
				log.Printf("Failed to unmarshal into %s: %v", task.ServiceType, err)
				continue
			}
			log.Printf("[Runner] CheckData: %+v", runnerInstance)
			serviceName = runnerInstance.(*checks.Vnc).Service.Name
		case "Web":
			runnerInstance = &checks.Web{}
			// Deserialize the check data into that runner instance
			if err := json.Unmarshal(task.CheckData, runnerInstance); err != nil {
				log.Printf("Failed to unmarshal into %s: %v", task.ServiceType, err)
				continue
			}
			log.Printf("[Runner] CheckData: %+v", runnerInstance)
			serviceName = runnerInstance.(*checks.Web).Service.Name
		case "WinRM":
			runnerInstance = &checks.WinRM{}
			// Deserialize the check data into that runner instance
			if err := json.Unmarshal(task.CheckData, runnerInstance); err != nil {
				log.Printf("Failed to unmarshal into %s: %v", task.ServiceType, err)
				continue
			}
			log.Printf("[Runner] CheckData: %+v", runnerInstance)
			serviceName = runnerInstance.(*checks.WinRM).Service.Name
		default:
			log.Printf("Unknown service type %s. Skipping.", task.ServiceType)
			continue
		}

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
			result.ServiceName = serviceName
			result.Status = false
			log.Printf("Runner internal timeout for service type: %s", task.ServiceType)
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
