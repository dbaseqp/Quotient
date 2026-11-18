package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/dbaseqp/Quotient/engine"
	"github.com/dbaseqp/Quotient/engine/checks"

	reaper "github.com/ramr/go-reaper"
	"github.com/redis/go-redis/v9"
)

// checkSubcommand makes the runner binary run a single check attempt (see runCheckProcess).
const checkSubcommand = "check"

const (
	// maxCheckStderr bounds how much of a check child's stderr is kept to explain a crash.
	maxCheckStderr = 64 << 10
	// maxCrashDebug bounds the crash explanation stored in a result's Debug.
	maxCrashDebug = 2 << 10
)

// Global variable to store the runner ID
var runnerID string

func main() {
	if len(os.Args) > 1 && os.Args[1] == checkSubcommand {
		// Only the result goes to stdout; anything else a check prints goes to stderr with the logs.
		out := os.Stdout
		os.Stdout = os.Stderr
		os.Exit(runCheckChild(os.Stdin, out))
	}

	// Use WithReaper to run reaper as PID 1 and application code in a child process
	// This prevents the reaper from interfering with processes we're actively managing
	reaper.WithReaper(reaper.Config{}, runApp)
}

func runApp(err error) int {
	if err != nil {
		slog.Error("error from reaper", "error", err)
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
		redisAddr = "quotient-redis:6379"
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: os.Getenv("REDIS_PASSWORD"),
	})
	ctx := context.Background()

	slog.Info("runner started", "runner_id", runnerID, "redis_addr", redisAddr)

	go func() {
		events := rdb.Subscribe(context.Background(), "events")
		// nolint:errcheck
		defer events.Close()
		eventsChannel := events.Channel()

		for msg := range eventsChannel {
			slog.Info("received message", "payload", msg.Payload)
			if msg.Payload == "reset" {
				slog.Info("reset event received, quitting")
				os.Exit(0)
			} else {
				continue
			}
		}
	}()

	for {
		task, err := getNextTask(ctx, rdb)
		if err != nil {
			slog.Error("error getting task", "error", err)
			continue
		}

		runner, err := createRunner(task)
		if err != nil {
			slog.Error("error creating runner", "error", err)
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

	slog.Info("received task", "round_id", task.RoundID, "team_id", task.TeamID,
		"team_identifier", task.TeamIdentifier, "service_type", task.ServiceType)

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

	slog.Debug("check data", "runner", fmt.Sprintf("%+v", runner))
	return runner, nil
}

func handleTask(ctx context.Context, rdb *redis.Client, runner checks.Runner, task *engine.Task) {
	taskKey := fmt.Sprintf("task:%t:%d:%d:%s:%s", task.Preflight, task.RoundID, task.TeamID, task.ServiceType, task.ServiceName)

	result := checks.Result{
		TeamID:      task.TeamID,
		ServiceName: task.ServiceName,
		ServiceType: task.ServiceType,
		RoundID:     task.RoundID,
		Preflight:   task.Preflight,
		Status:      false,
		RunnerID:    runnerID,
		StartTime:   time.Now().Format(time.RFC3339),
		StatusText:  "running",
		// From the definition, not the check's answer: a check the round deadline cuts off never answers, and the
		// export's max_points sums stored points.
		Points: runner.GetPoints(),
	}

	// Set initial "running" status in Redis for visualization
	statusJSON, _ := json.Marshal(result)
	rdb.Set(ctx, taskKey, statusJSON, time.Until(task.Deadline))

	// this currently discards all failed attempts
	for i := range task.Attempts {
		slog.Info("running check", "round_id", task.RoundID, "team_id", task.TeamID,
			"service_type", task.ServiceType, "service_name", task.ServiceName, "attempt", i+1)

		// Create context with deadline
		checkCtx, cancel := context.WithDeadline(ctx, task.Deadline)
		defer cancel()

		// Only the fields a check produces are taken from it; task identity, points and runner metadata stay as set
		// above.
		r, err := runCheckProcess(checkCtx, task)
		switch {
		case err == nil:
			result.Status, result.Error, result.Debug, result.Target = r.Status, r.Error, r.Debug, r.Target

			slog.Info("check result received", "round_id", result.RoundID, "team_id", result.TeamID,
				"service_type", result.ServiceType, "status", result.Status, "debug", result.Debug, "error", result.Error)

		case checkCtx.Err() != nil:
			result.Status = false
			result.Debug = "round ended before check completed"
			result.Error = "timeout"

			slog.Warn("check timed out", "round_id", task.RoundID, "team_id", task.TeamID,
				"service_type", task.ServiceType)

		default:
			result.Status = false
			result.Debug = err.Error()
			result.Error = "check crashed"

			slog.Error("check crashed", "round_id", task.RoundID, "team_id", task.TeamID,
				"service_type", task.ServiceType, "service_name", task.ServiceName, "error", err)
		}

		// Break if successful or deadline passed
		if result.Status || time.Now().After(task.Deadline) {
			break
		}
		if i < task.Attempts-1 && !waitForRetry(ctx, time.Duration(task.AttemptDelay)*time.Second, task.Deadline) {
			break
		}
	}

	result.EndTime = time.Now().Format(time.RFC3339)
	result.StatusText = map[bool]string{true: "success", false: "failed"}[result.Status]
	resultJSON, err := json.Marshal(result)
	if err != nil {
		slog.Error("failed to marshal result", "error", err)
		return
	}

	if err := rdb.RPush(ctx, "results", resultJSON).Err(); err != nil {
		slog.Error("failed to push result to Redis", "error", err)
		return
	}

	slog.Info("successfully pushed result", "round_id", result.RoundID, "team_id", result.TeamID,
		"service_type", result.ServiceType, "status", result.Status)

	// Use a longer TTL for completed tasks to ensure they're visible in the UI
	rdb.Set(ctx, taskKey, resultJSON, 3*time.Minute)
}

// runCheckProcess runs one check attempt for task in a child process (the runner binary re-executed as
// "runner check", see runCheckChild) and returns the check's result. Check libraries parse team-controlled replies,
// some in goroutines of their own where a panic cannot be recovered; in the child such a crash fails only this
// attempt instead of killing the runner with every task on it. The child, and anything it started, is killed when
// ctx ends.
func runCheckProcess(ctx context.Context, task *engine.Task) (checks.Result, error) {
	var result checks.Result
	exe, err := os.Executable()
	if err != nil {
		return result, fmt.Errorf("failed to locate runner executable: %w", err)
	}
	taskJSON, err := json.Marshal(task)
	if err != nil {
		return result, fmt.Errorf("failed to marshal task: %w", err)
	}

	var stdout bytes.Buffer
	stderr := &tailWriter{max: maxCheckStderr}
	cmd := exec.CommandContext(ctx, exe, checkSubcommand) // #nosec G204 -- re-executes this runner binary
	cmd.Stdin = bytes.NewReader(taskJSON)
	cmd.Stdout = &stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, stderr)
	// Own process group, so ending the attempt also stops what the check started (custom check scripts).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second

	if err := cmd.Run(); err != nil {
		return result, fmt.Errorf("%w: %s", err, crashReport(stderr.buf))
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return result, fmt.Errorf("invalid check result: %w", err)
	}
	return result, nil
}

// runCheckChild is the child side of runCheckProcess: it reads the task from in, runs one check attempt and writes
// the check's result to out. It returns the process exit code.
func runCheckChild(in io.Reader, out io.Writer) int {
	var task engine.Task
	if err := json.NewDecoder(in).Decode(&task); err != nil {
		slog.Error("invalid task", "error", err)
		return 1
	}
	runner, err := createRunner(&task)
	if err != nil {
		slog.Error("error creating runner", "error", err)
		return 1
	}

	// Set credentials from task payload for the checks to use (per-instance, thread-safe)
	if len(task.Credentials) > 0 {
		creds := make([]checks.TaskCredential, len(task.Credentials))
		for i, c := range task.Credentials {
			creds[i] = checks.TaskCredential{
				Username: c.Username,
				Password: c.Password,
				Credlist: c.Credlist,
			}
		}
		runner.SetTaskCredentials(creds)
	}

	resultsChan := make(chan checks.Result, 1)
	runner.Run(task.TeamID, task.TeamIdentifier, task.RoundID, resultsChan)
	if err := json.NewEncoder(out).Encode(<-resultsChan); err != nil {
		slog.Error("failed to write check result", "error", err)
		return 1
	}
	return 0
}

// tailWriter keeps the last max bytes written to it.
type tailWriter struct {
	buf []byte
	max int
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.max {
		w.buf = w.buf[len(w.buf)-w.max:]
	}
	return len(p), nil
}

// crashReport explains a failed check child from the end of its stderr: the Go panic or fatal error report when
// there is one, otherwise the last output.
func crashReport(stderr []byte) string {
	start := max(len(stderr)-maxCrashDebug, 0)
	for _, marker := range []string{"panic: ", "fatal error: "} {
		if i := bytes.Index(append([]byte("\n"), stderr...), []byte("\n"+marker)); i >= 0 {
			start = i
			break
		}
	}
	report := stderr[start:]
	if len(report) > maxCrashDebug {
		report = report[:maxCrashDebug]
	}
	return string(bytes.TrimSpace(report))
}

// waitForRetry waits delay before the next attempt and reports whether that
// attempt can still start before the round deadline.
func waitForRetry(ctx context.Context, delay time.Duration, deadline time.Time) bool {
	if !time.Now().Add(delay).Before(deadline) {
		return false
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
