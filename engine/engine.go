package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/dbaseqp/Quotient/engine/config"
	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/redis/go-redis/v9"
)

type ScoringEngine struct {
	// Config is shared with the web server. Take one Current() snapshot per
	// unit of work: a hot reload swaps in a new snapshot at any time.
	Config *config.Live
	// credentialsMutex holds one lock per team. Seeding runs on the engine
	// goroutine while the web server serves, so credentialsMu guards the map.
	credentialsMutex      map[uint]*sync.Mutex
	credentialsMu         sync.RWMutex
	UptimePerService      map[uint]map[string]db.Uptime
	uptimeMu              sync.RWMutex
	SlaPerService         map[uint]map[string]int
	CurrentRound          uint
	NextRoundStartTime    time.Time
	CurrentRoundStartTime time.Time
	RedisClient           *redis.Client

	roundListenersMu sync.Mutex
	roundListeners   map[chan struct{}]struct{}
	DB               *db.DB

	// Concurrency control for materialized view refresh
	Refreshing atomic.Bool

	// Config update handling
	configPath string

	pauseMu sync.Mutex
	// resume is closed while the engine runs; while paused it is open and the round loop blocks on it.
	resume chan struct{}
	// scheduleChanged is closed and replaced whenever the competition schedule changes.
	scheduleChanged chan struct{}
	// pausedAtEnd is the competition end the round loop paused itself at (nil
	// for a manual pause); owned by the round loop, which alone decides overtime.
	pausedAtEnd *time.Time

	// roundFunc is called each round. Set by Start() from the event type, or
	// overridden by NewTestEngine to inject faults without touching the DB.
	roundFunc func() error
}
var errReset = errors.New("reset event received")


func NewEngine(configPath string) *ScoringEngine {
	// Ensure config is properly validated and runners are set up
	live := &config.Live{}
	if err := live.Reload(configPath); err != nil {
		panic(fmt.Sprintf("Failed to validate initial config: %v", err))
	}
	conf := live.Current()

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "quotient-redis:6379"
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: os.Getenv("REDIS_PASSWORD"),
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		panic(fmt.Sprintf("Failed to connect to Redis: %v", err))
	}

	d := db.Connect(conf.RequiredSettings.DBConnectURL)

	if err := d.AddTeams(conf); err != nil {
		panic(fmt.Sprintf("Failed to add teams to DB: %v", err))
	}

	se := &ScoringEngine{
		Config:           live,
		credentialsMutex: make(map[uint]*sync.Mutex),
		UptimePerService: make(map[uint]map[string]db.Uptime),
		SlaPerService:    make(map[uint]map[string]int),
		RedisClient:      rdb,
		configPath:       configPath,
		DB:               d,
	}
	se.resetPauseState()

	if err := live.WatchConfig(configPath, func() {
		if err := se.DB.AddTeams(se.Config.Current()); err != nil {
			slog.Error("failed to add teams on config reload", "error", err)
		}
		if err := se.EnsureCredentialsSeeded(); err != nil {
			slog.Error("failed to seed credentials on config reload", "error", err)
		}
		se.signalScheduleChanged()
	}); err != nil {
		slog.Error("Failed to start config watcher", "error", err)
	}

	return se
}

func (se *ScoringEngine) Start() {
	if t, err := se.DB.GetLastRound(); err != nil {
		slog.Error("failed to get last round", "error", err)
	} else {
		se.CurrentRound = uint(t.ID) + 1
	}

	se.uptimeMu.Lock()
	if err := se.DB.LoadUptimes(&se.UptimePerService); err != nil {
		slog.Error("failed to load uptimes", "error", err)
	}
	se.uptimeMu.Unlock()

	if err := se.DB.LoadSLAs(&se.SlaPerService, se.Config.Current().MiscSettings.SlaThreshold); err != nil {
		slog.Error("failed to load SLAs", "error", err)
	}

	// load credentials
	err := se.EnsureCredentialsSeeded()
	if err != nil {
		slog.Error("failed to load credential files into teams", "error", err)
	}

	se.NextRoundStartTime = time.Time{}
	se.pausedAtEnd = nil

	stop := make(chan struct{})
	loopDone := make(chan struct{})

	// engine loop
	go func() {
		defer close(loopDone)
		for {
			slog.Info("Queueing up for round", "round", se.CurrentRound)
			schedule, ok := se.waitForRoundReady(stop)
			if !ok {
				return
			}
			select {
			case <-stop:
				return
			default:
			}
			if se.pauseAtCompetitionEnd(schedule) {
				continue
			}
			conf := se.Config.Current()
			se.CurrentRoundStartTime = time.Now()
			se.NextRoundStartTime = time.Now().Add(time.Duration(conf.MiscSettings.Delay) * time.Second)

		var err error
		if se.roundFunc != nil {
			err = se.roundFunc()
		} else {
			switch conf.RequiredSettings.EventType {
			case "koth":
				err = se.koth()
			case "rvb":
				if se.PreflightPending() {
					err = se.preflight()
				} else {
					err = se.rvb()
				}
			default:
				slog.Error("Unknown event type", "eventType", conf.RequiredSettings.EventType)
			}
		}
		if err != nil {
			if errors.Is(err, errReset) {
				slog.Info("Round loop exiting for reset")
				return
			}
			slog.Error("Round error, retrying next round", "error", err)
		}
		// Divisor rotates the runners' source addresses when it sees round_finish. A round that ran to
		// its deadline (a lost or late result) would otherwise start the next round at once, while the
		// rotation removes the address the new round's first checks connect from, and those checks
		// time out against services that are up.
		if earliest := time.Now().Add(roundFinishSettle); se.NextRoundStartTime.Before(earliest) {
			se.NextRoundStartTime = earliest
		}

			slog.Info(fmt.Sprintf("Next round will start in %s, sleeping...", time.Until(se.NextRoundStartTime).String()))
		}
	}()
	se.waitForReset()
	close(stop)
	<-loopDone
	slog.Info("Restarting scoring...")
}

// roundFinishSettle is the least time between a round_finish and the next round's start. Divisor
// rotates runner source addresses on round_finish (about 1 s per address it probes, NUM_IPS of them),
// so this only delays the round after one that ran to its deadline.
const roundFinishSettle = 5 * time.Second

func (se *ScoringEngine) waitForReset() {
	// wait for a signal to reset the engine
	// this will block until the engine is reset
	// this is a blocking call
	// the engine will be reset and the loop will start again

	events := se.RedisClient.Subscribe(context.Background(), "events")
	// nolint:errcheck
	defer events.Close()
	eventsChannel := events.Channel()

	for msg := range eventsChannel {
		slog.Info("Received message", "message", msg.Payload)
		if msg.Payload == "reset" {
			slog.Info("Reset event received, quitting...")
			return
		} else {
			continue
		}
	}
}

func (se *ScoringEngine) SubscribeRounds() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	se.roundListenersMu.Lock()
	if se.roundListeners == nil {
		se.roundListeners = make(map[chan struct{}]struct{})
	}
	se.roundListeners[ch] = struct{}{}
	se.roundListenersMu.Unlock()

	return ch, func() {
		se.roundListenersMu.Lock()
		delete(se.roundListeners, ch)
		se.roundListenersMu.Unlock()
	}
}

func (se *ScoringEngine) notifyRoundListeners() {
	se.roundListenersMu.Lock()
	defer se.roundListenersMu.Unlock()

	for ch := range se.roundListeners {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (se *ScoringEngine) LastRound() (db.RoundSchema, bool, error) {
	if se.PreflightPending() {
		r, err := se.DB.GetLastPreflightRound()
		return r, true, err
	}
	r, err := se.DB.GetLastRound()
	return r, false, err
}

func (se *ScoringEngine) WithUptimes(fn func(map[uint]map[string]db.Uptime)) error {
	if se.PreflightPending() {
		u, err := se.DB.GetPreflightUptimes()
		if err != nil {
			return err
		}
		fn(u)
		return nil
	}
	se.uptimeMu.RLock()
	defer se.uptimeMu.RUnlock()
	fn(se.UptimePerService)
	return nil
}

func populateSummaryUptimes(summaries []db.ServiceSummary, uptimes map[string]db.Uptime) {
	for i := range summaries {
		if u := uptimes[summaries[i].ServiceName]; u.TotalChecks > 0 {
			summaries[i].Uptime = float64(u.PassedChecks) / float64(u.TotalChecks)
		}
	}
}

func (se *ScoringEngine) TeamSummary(teamID uint) ([]db.ServiceSummary, error) {
	if se.PreflightPending() {
		summaries, err := se.DB.GetPreflightTeamSummary(teamID)
		if err != nil || len(summaries) == 0 {
			return summaries, err
		}
		uptimes, err := se.DB.GetPreflightUptimes()
		if err != nil {
			return nil, err
		}
		populateSummaryUptimes(summaries, uptimes[teamID])
		return summaries, nil
	}
	summaries, err := se.DB.GetTeamSummary(teamID)
	if err != nil || len(summaries) == 0 {
		return summaries, err
	}
	se.uptimeMu.RLock()
	populateSummaryUptimes(summaries, se.UptimePerService[teamID])
	se.uptimeMu.RUnlock()
	return summaries, nil
}

func (se *ScoringEngine) ServiceAllChecksByTeam(teamID uint, serviceID string) ([]db.ServiceCheckSchema, error) {
	if se.PreflightPending() {
		return se.DB.GetPreflightServiceAllChecksByTeam(teamID, serviceID)
	}
	return se.DB.GetServiceAllChecksByTeam(teamID, serviceID)
}

// GetActiveTasks returns all active and recently completed tasks
func (se *ScoringEngine) GetActiveTasks() (map[string]any, error) {
	ctx := context.Background()

	// Default empty response structure
	result := map[string]any{
		"running":     []any{},
		"success":     []any{},
		"failed":      []any{},
		"all_runners": []any{},
	}

	// Get all task keys using a single pattern
	allKeys, err := se.RedisClient.Keys(ctx, "task:*").Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get tasks: %w", err)
	}

	// Skip if no keys found
	if len(allKeys) == 0 {
		return result, nil
	}

	// Use a single pipeline to get all task data in one round-trip
	pipe := se.RedisClient.Pipeline()
	cmds := make(map[string]*redis.StringCmd, len(allKeys))

	// Queue up all GET commands at once
	for _, key := range allKeys {
		cmds[key] = pipe.Get(ctx, key)
	}

	// Execute the pipeline
	_, err = pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("failed to execute pipeline: %w", err)
	}

	// Use map as a set to track unique runner IDs
	runnersSet := make(map[string]struct{})

	// Process all results
	for _, cmd := range cmds {
		value, err := cmd.Result()
		if err != nil {
			continue // Skip if we can't get the task details
		}

		// Parse the value as JSON directly into a checks.Result
		var taskStatus checks.Result
		if err := json.Unmarshal([]byte(value), &taskStatus); err != nil {
			continue // Skip if we can't parse the JSON
		}

		// Add runner ID to set if available
		if taskStatus.RunnerID != "" {
			runnersSet[taskStatus.RunnerID] = struct{}{}
		}

		// Categorize by status - directly using the status as map key for cleaner code
		if statusKey := taskStatus.StatusText; statusKey == "running" || statusKey == "success" || statusKey == "failed" {
			result[statusKey] = append(result[statusKey].([]any), taskStatus)
		}
	}

	// Convert runners set to slice in one go
	for runnerId := range runnersSet {
		result["all_runners"] = append(result["all_runners"].([]any), runnerId)
	}

	return result, nil
}

func (se *ScoringEngine) resetPauseState() {
	se.pauseMu.Lock()
	defer se.pauseMu.Unlock()
	se.resume = make(chan struct{})
	if !se.Config.Current().MiscSettings.StartPaused {
		close(se.resume)
	}
	se.scheduleChanged = make(chan struct{})
}

func (se *ScoringEngine) pausedLocked() bool {
	select {
	case <-se.resume:
		return false
	default:
		return true
	}
}

func (se *ScoringEngine) SetPaused(pause bool) bool {
	se.pauseMu.Lock()
	defer se.pauseMu.Unlock()
	if se.pausedLocked() == pause {
		return false
	}
	if pause {
		se.resume = make(chan struct{})
	} else {
		close(se.resume)
	}
	return true
}

func (se *ScoringEngine) Paused() bool {
	se.pauseMu.Lock()
	defer se.pauseMu.Unlock()
	return se.pausedLocked()
}

// Schedule is the effective competition schedule — the runtime schedule set
// through the API when there is one, else event.conf — and where the
// competition stands against it.
type Schedule struct {
	Start, End *time.Time
	// Source is "runtime", "config" or "none".
	Source string
	// Started is whether the competition is open to teams right now.
	Started bool
	// StartedAt is the first start, the anchor of imported injects; nil until then.
	StartedAt *time.Time
	// OvertimeEnd is the end an admin resumed scoring after; scoring continues
	// past End while they are equal.
	OvertimeEnd *time.Time
}

// scheduleRetryDelay is how soon the round loop re-reads the schedule after a
// failed read or a failed scheduled start.
const scheduleRetryDelay = 5 * time.Second

// effectiveSchedule merges the persisted state with event.conf: a runtime
// schedule (any saved start or end) replaces the configured one as a whole.
func effectiveSchedule(state db.CompetitionStateSchema, conf config.InjectConfig) Schedule {
	s := Schedule{Source: "none", Started: state.Started, StartedAt: state.StartedAt, OvertimeEnd: state.OvertimeEnd}
	switch {
	case state.RuntimeScheduled():
		s.Start, s.End, s.Source = state.ScheduledStart, state.ScheduledEnd, "runtime"
	case conf.Start != nil || conf.End != nil:
		s.Start, s.End, s.Source = conf.Start, conf.End, "config"
	}
	return s
}

// nextScheduledStart reports the scheduled start the round loop still has to
// perform. Nothing is pending once the competition has ever started or its
// end has passed.
func (s Schedule) nextScheduledStart() (start time.Time, pending bool) {
	if s.StartedAt != nil || s.Start == nil {
		return time.Time{}, false
	}
	if s.End != nil && !time.Now().Before(*s.End) {
		return time.Time{}, false
	}
	return *s.Start, true
}

// ErrEndBeforeStart rejects a schedule whose end is not after its start.
var ErrEndBeforeStart = errors.New("competition end time must be after start time")

func (se *ScoringEngine) CompetitionSchedule() (Schedule, error) {
	state, err := se.DB.GetCompetitionState()
	if err != nil {
		return Schedule{}, err
	}
	return effectiveSchedule(state, se.Config.Current().InjectSettings), nil
}

// SetCompetitionSchedule stores a runtime schedule that replaces event.conf's
// as a whole; both nil clears it so event.conf applies again.
func (se *ScoringEngine) SetCompetitionSchedule(start, end *time.Time) error {
	if start != nil && end != nil && !end.After(*start) {
		return ErrEndBeforeStart
	}
	next := effectiveSchedule(db.CompetitionStateSchema{ScheduledStart: start, ScheduledEnd: end}, se.Config.Current().InjectSettings)
	noStart, err := se.DB.SetCompetitionSchedule(start, end, next.Start, next.End)
	if err != nil {
		return err
	}
	if noStart {
		slog.Warn("competition schedule has no start; imported inject times keep their previous anchor")
	}
	se.signalScheduleChanged()
	return nil
}

// SetCompetitionStarted opens or closes the competition to teams by hand.
func (se *ScoringEngine) SetCompetitionStarted(started bool) error {
	if err := se.DB.SetCompetitionStarted(started); err != nil {
		return err
	}
	se.signalScheduleChanged()
	return nil
}

// signalScheduleChanged wakes the round loop to re-read the schedule.
func (se *ScoringEngine) signalScheduleChanged() {
	se.pauseMu.Lock()
	close(se.scheduleChanged)
	se.scheduleChanged = make(chan struct{})
	se.pauseMu.Unlock()
}

// startScheduledCompetition performs a due scheduled start if the competition
// was never started, resuming scoring when it does. It reports false only when
// the write failed.
func (se *ScoringEngine) startScheduledCompetition(start time.Time) bool {
	started, err := se.DB.StartCompetitionOnce(start)
	if err != nil {
		slog.Error("failed to start competition at scheduled start", "error", err)
		return false
	}
	if started {
		slog.Info("Competition start reached, starting competition", "start", start)
		se.SetPaused(false)
	}
	return true
}

// waitForRoundReady blocks until the next round may start: the engine is not
// paused, NextRoundStartTime has passed and the schedule was read in the same
// pass. While waiting it performs a due scheduled competition start, whether
// paused or between rounds. It returns that schedule, and false once stop is
// closed.
func (se *ScoringEngine) waitForRoundReady(stop <-chan struct{}) (Schedule, bool) {
	for {
		var resume <-chan struct{}
		var roundDue <-chan time.Time
		se.pauseMu.Lock()
		if se.pausedLocked() {
			resume = se.resume
		} else {
			roundDue = time.After(time.Until(se.NextRoundStartTime))
		}
		scheduleChanged := se.scheduleChanged
		se.pauseMu.Unlock()

		var startDue, retry <-chan time.Time
		schedule, err := se.CompetitionSchedule()
		if err != nil {
			// Never start a round blind: wait for the retry instead of the round timer.
			slog.Error("failed to read competition schedule; retrying", "error", err, "in", scheduleRetryDelay)
			roundDue, retry = nil, time.After(scheduleRetryDelay)
		} else if start, pending := schedule.nextScheduledStart(); pending {
			if wait := time.Until(start); wait > 0 {
				startDue = time.After(wait)
			} else if se.startScheduledCompetition(start) {
				continue
			} else {
				retry = time.After(scheduleRetryDelay)
			}
		}

		select {
		case <-stop:
			return Schedule{}, false
		case <-resume:
		case <-roundDue:
			// A pause issued during the inter-round sleep must hold before the
			// next round; it does not wake this select.
			if !se.Paused() {
				return schedule, true
			}
		case <-scheduleChanged:
		case <-startDue:
		case <-retry:
		}
	}
}

// pauseAtCompetitionEnd pauses once the effective end has passed unless an
// admin resumed after that end; a resume from its own end-pause is recorded
// here as overtime (OvertimeEnd), which a score reset or a runtime schedule
// write with a different end clears.
func (se *ScoringEngine) pauseAtCompetitionEnd(s Schedule) bool {
	if se.pausedAtEnd != nil && (s.End == nil || !se.pausedAtEnd.Equal(*s.End)) {
		se.pausedAtEnd = nil
	}
	if s.End == nil || time.Now().Before(*s.End) {
		return false
	}
	end := *s.End
	if s.OvertimeEnd != nil && s.OvertimeEnd.Equal(end) {
		return false
	}
	if se.pausedAtEnd != nil {
		if err := se.DB.SetOvertimeEnd(&end); err != nil {
			slog.Error("failed to save competition overtime; retrying next round", "error", err)
			return false
		}
		se.pausedAtEnd = nil
		return false
	}
	se.pauseMu.Lock()
	defer se.pauseMu.Unlock()
	if se.pausedLocked() {
		return true
	}
	se.resume = make(chan struct{})
	se.pausedAtEnd = &end
	slog.Info("Competition end reached, pausing scoring", "end", end)
	return true
}

func (se *ScoringEngine) ResetScores() error {
	slog.Info("Resetting scores and clearing Redis queues")

	// Reset the database
	if err := se.DB.ResetScores(); err != nil {
		slog.Error("failed to reset scores", "error", err)
		return fmt.Errorf("failed to reset scores: %v", err)
	}

	// Flush Redis queues
	ctx := context.Background()
	keysToDelete := []string{"tasks", "results"}
	for _, key := range keysToDelete {
		if err := se.RedisClient.Del(ctx, key).Err(); err != nil {
			slog.Error("Failed to clear Redis queue", "queue", key, "error", err)
			return fmt.Errorf("failed to clear Redis queue %s: %v", key, err)
		}
	}

	// Reset engine state
	se.RedisClient.Publish(context.Background(), "events", "reset")

	se.CurrentRound = 1
	se.uptimeMu.Lock()
	se.UptimePerService = make(map[uint]map[string]db.Uptime)
	se.uptimeMu.Unlock()
	se.SlaPerService = make(map[uint]map[string]int)
	se.notifyRoundListeners()
	slog.Info("Scores reset and Redis queues cleared successfully")

	return nil
}

func (se *ScoringEngine) koth() error {
	se.finishScoredRound()
	return nil
}

// perform a round of rvb
func (se *ScoringEngine) rvb() error {
	slog.Info("Starting round", "round", se.CurrentRound)
	conf := se.Config.Current()
	results, err := se.runChecks(conf, se.CurrentRound, false)
	if err != nil {
		return err
	}
	se.processCollectedResults(conf, results)
	se.finishScoredRound()
	return nil
}

func (se *ScoringEngine) finishScoredRound() {
	slog.Info(fmt.Sprintf("Round %d complete", se.CurrentRound))
	se.CurrentRound++
	se.announceRoundFinish()
}

// announceRoundFinish tells Divisor to rotate runner IPs and SSE clients to refresh.
func (se *ScoringEngine) announceRoundFinish() {
	se.RedisClient.Publish(context.Background(), "events", "round_finish")
	se.notifyRoundListeners()
}

// PreflightPending reads the DB rather than CurrentRound (which advances on
// empty rounds and resets on restart), returning true on DB read error.
func (se *ScoringEngine) PreflightPending() bool {
	if se.DB.GetCompetitionStarted() {
		return false
	}
	scored, err := se.DB.HasScoredRound()
	if err != nil {
		slog.Error("failed to check for scored rounds, running a preflight round", "error", err)
		return true
	}
	return !scored
}

// preflight runs the same checks as rvb but stores the results apart from
// scoring.
func (se *ScoringEngine) preflight() error {
	last, err := se.DB.GetLastPreflightRoundID()
	if err != nil {
		return err
	}
	round := db.PreflightRoundSchema{ID: last + 1, StartTime: se.CurrentRoundStartTime}
	slog.Info("Starting preflight round", "preflight_round", round.ID)
	results, err := se.runChecks(se.Config.Current(), round.ID, true)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		slog.Warn("No results collected for preflight round", "preflight_round", round.ID)
	}
	for _, result := range results {
		round.Checks = append(round.Checks, db.PreflightCheckSchema{
			TeamID:      result.TeamID,
			ServiceName: sanitizeDBString(result.ServiceName),
			ServiceType: result.ServiceType,
			Result:      result.Status,
			Error:       sanitizeDBString(result.Error),
			Debug:       sanitizeDBString(result.Debug),
		})
	}
	if err := se.DB.CreatePreflightRound(round); err != nil {
		slog.Error("failed to save preflight round", "preflight_round", round.ID, "error", err)
	}
	slog.Info(fmt.Sprintf("Preflight round %d complete", round.ID))
	se.announceRoundFinish()
	return nil
}

// runChecks enqueues the checks of conf, one config snapshot for the whole round.
func (se *ScoringEngine) runChecks(conf *config.ConfigSettings, roundID uint, preflight bool) ([]checks.Result, error) {
	// reassign the next round start time with jitter
	// double the jitter and subtract it to get a random number between -jitter and jitter
	randomJitter := rand.Intn(2*conf.MiscSettings.Jitter) - conf.MiscSettings.Jitter // #nosec G404 -- non-crypto randomization for timing jitter
	jitter := time.Duration(randomJitter) * time.Second
	se.NextRoundStartTime = time.Now().Add(time.Duration(conf.MiscSettings.Delay) * time.Second).Add(jitter)

	slog.Info(fmt.Sprintf("round should take %s", time.Until(se.NextRoundStartTime).String()))

	events := se.RedisClient.Subscribe(context.Background(), "events")
	// nolint:errcheck
	defer events.Close()
	eventsChannel := events.Channel()

	teams, err := se.DB.GetTeams()
	if err != nil {
		slog.Error("failed to get teams:", "error", err)
		return nil, err
	}

	// expected holds the key of every task of the round. A result is accepted only
	// for a key still in it, so a duplicate or stray result (e.g. from a check
	// the previous engine process started under the same round ID) can neither
	// be counted twice nor crowd out another team's result.
	expected := make(map[resultKey]struct{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Until(se.NextRoundStartTime))
	defer cancel()

	slog.Debug("Starting service checks", "round", roundID, "preflight", preflight)
	allRunners := conf.AllChecks()
	slog.Info("Service check configuration",
		"boxes", len(conf.Box),
		"runners", len(allRunners))

	// Log details about each box's runners
	for _, box := range conf.Box {
		slog.Debug("Box configuration", "name", box.Name, "runners", len(box.Runners))
	}

	// Clear any stale tasks and results before enqueuing new ones. After a
	// restart mid-round the next round reuses the round ID, so leftover
	// results would otherwise be counted as this round's.
	for _, key := range []string{"tasks", "results"} {
		if stale := se.RedisClient.LLen(ctx, key).Val(); stale > 0 {
			slog.Warn("Clearing stale entries from queue", "queue", key, "count", stale, "round", roundID, "preflight", preflight)
			se.RedisClient.Del(ctx, key)
		}
	}

	for _, team := range teams {
		if !team.Active {
			continue
		}
		for _, r := range allRunners {
			if !r.Runnable() {
				continue
			}
			enabled, err := se.DB.IsTeamServiceEnabled(team.ID, r.GetName())
			if err != nil {
				slog.Error("failed to check service state", "team", team.ID, "service", r.GetName(), "error", err)
				continue
			}
			if !enabled {
				continue
			}
			// serialize the entire check definition to JSON
			data, err := json.Marshal(r)
			if err != nil {
				slog.Error("failed to marshal check definition", "error", err)
				continue
			}

			task := Task{
				TeamID:         team.ID,
				TeamIdentifier: team.Identifier,
				ServiceType:    r.GetType(),
				ServiceName:    r.GetName(),
				RoundID:        roundID,
				Preflight:      preflight,
				Deadline:       se.NextRoundStartTime,
				Attempts:       r.GetAttempts(),
				AttemptDelay:   r.GetAttemptDelay(),
				CheckData:      data, // the entire specialized struct
			}

			// Populate credentials if check needs them
			if credlists := r.GetCredlists(); len(credlists) > 0 {
				for _, credlistName := range credlists {
					creds, err := se.DB.GetTeamCredentials(team.ID, credlistName)
					if err != nil {
						slog.Error("failed to get credentials for task", "team", team.ID, "credlist", credlistName, "error", err)
						continue
					}
					for _, c := range creds {
						task.Credentials = append(task.Credentials, Credential{
							Username: c.Username,
							Password: c.Password,
							Credlist: credlistName,
						})
					}
				}
				// Skip task if check requires credentials but none were loaded
				if len(task.Credentials) == 0 {
					slog.Error("skipping check: requires credentials but none seeded", "service", r.GetName(), "team", team.ID)
					continue
				}
			}

			payload, err := json.Marshal(task)
			if err != nil {
				slog.Error("failed to marshal service task", "error", err)
				continue
			}
			// A task whose RPUSH failed can never report, so its key stays
			// expected and the round is discarded at the deadline.
			if err := se.RedisClient.RPush(ctx, "tasks", payload).Err(); err != nil {
				slog.Error("failed to enqueue service task", "team", team.ID, "service", r.GetName(), "error", err)
			}
			expected[resultKey{team.ID, r.GetName()}] = struct{}{}
		}
	}
	slog.Info("Enqueued checks", "count", len(expected))

	// Runners give up at the task deadline and then push a timeout result, so
	// keep collecting a little past it. BLPOP waits whole seconds, so poll in
	// one-second slices and decide on the wall clock, not on a BLPOP timeout.
	collectUntil := se.NextRoundStartTime.Add(resultGracePeriod)
	results := make([]checks.Result, 0, len(expected))
	for len(expected) > 0 && time.Now().Before(collectUntil) {
		select {
		case msg := <-eventsChannel:
			slog.Info("Received message", "message", msg.Payload)
			if msg.Payload == "reset" {
				slog.Info("Reset event received, quitting...")
				return nil, errReset
			}
			continue
		default:
		}

		val, err := se.RedisClient.BLPop(context.Background(), time.Second, "results").Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			slog.Error("Failed to fetch results from Redis:", "error", err)
			time.Sleep(min(time.Second, time.Until(collectUntil)))
			continue
		}

		// val[0] = "results", val[1] = JSON checks.Result
		if len(val) < 2 {
			slog.Warn("Malformed result from Redis", "val", val)
			continue
		}

		var result checks.Result
		if err := json.Unmarshal([]byte(val[1]), &result); err != nil {
			slog.Error("Failed to unmarshal check result", "error", err)
			continue
		}
		if result.RoundID != roundID || result.Preflight != preflight {
			slog.Warn("Ignoring out of round result", "receivedRound", result.RoundID, "receivedPreflight", result.Preflight, "currentRound", roundID, "currentPreflight", preflight)
			continue
		}
		key := resultKey{result.TeamID, result.ServiceName}
		if _, ok := expected[key]; !ok {
			slog.Warn("Ignoring duplicate or unexpected result", "round", roundID, "team_id", result.TeamID, "service_name", result.ServiceName)
			continue
		}
		delete(expected, key)
		results = append(results, result)
		slog.Debug("service check finished", "round_id", result.RoundID, "team_id", result.TeamID, "service_name", result.ServiceName, "result", result.Status, "debug", result.Debug, "error", result.Error)
	}

	if len(expected) > 0 {
		// If one team can't be scored, no team is: discard the whole round.
		missing := make([]string, 0, len(expected))
		for key := range expected {
			missing = append(missing, fmt.Sprintf("team %d %s", key.teamID, key.service))
		}
		slog.Warn("Checks without a result by the round deadline, discarding round", "missing", missing, "collected", len(results), "round", roundID, "preflight", preflight)
		return []checks.Result{}, nil
	}

	return results, nil
}

// resultGracePeriod is how long past the task deadline the engine keeps
// collecting, so the timeout result a runner pushes at the deadline still counts.
const resultGracePeriod = 2 * time.Second

type resultKey struct {
	teamID  uint
	service string
}

func sanitizeDBString(s string) string {
	// remove nulls
	s = strings.ReplaceAll(s, "\x00", "")
	// if invalid UTF-8, replace with �
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	return s
}

func (se *ScoringEngine) processCollectedResults(conf *config.ConfigSettings, results []checks.Result) {
	if len(results) == 0 {
		slog.Warn("No results collected for round", "round", se.CurrentRound)
		return
	}

	dbResults := []db.ServiceCheckSchema{}

	for _, result := range results {
		dbResults = append(dbResults, db.ServiceCheckSchema{
			TeamID:      result.TeamID,
			RoundID:     uint(se.CurrentRound),
			ServiceName: sanitizeDBString(result.ServiceName),
			Points:      result.Points,
			Result:      result.Status,
			Error:       sanitizeDBString(result.Error),
			Debug:       sanitizeDBString(result.Debug),
		})
	}

	if len(dbResults) == 0 {
		slog.Warn("No results to process for the current round", "round", se.CurrentRound)
		return
	}

	// Save results to database
	round := db.RoundSchema{
		ID:        uint(se.CurrentRound),
		StartTime: se.CurrentRoundStartTime,
		Checks:    dbResults,
	}
	if _, err := se.DB.CreateRound(round); err != nil {
		slog.Error("failed to create round:", "round", se.CurrentRound, "error", err)
		return
	}

	se.uptimeMu.Lock()
	for _, result := range results {
		// Update uptime and SLA maps
		if _, ok := se.UptimePerService[result.TeamID]; !ok {
			se.UptimePerService[result.TeamID] = make(map[string]db.Uptime)
		}
		if _, ok := se.UptimePerService[result.TeamID][result.ServiceName]; !ok {
			se.UptimePerService[result.TeamID][result.ServiceName] = db.Uptime{}
		}
		newUptime := se.UptimePerService[result.TeamID][result.ServiceName]
		if result.Status {
			newUptime.PassedChecks++
		}
		newUptime.TotalChecks++
		se.UptimePerService[result.TeamID][result.ServiceName] = newUptime

		if _, ok := se.SlaPerService[result.TeamID]; !ok {
			se.SlaPerService[result.TeamID] = make(map[string]int)
		}
		if _, ok := se.SlaPerService[result.TeamID][result.ServiceName]; !ok {
			se.SlaPerService[result.TeamID][result.ServiceName] = 0
		}
		if result.Status {
			se.SlaPerService[result.TeamID][result.ServiceName] = 0
		} else {
			se.SlaPerService[result.TeamID][result.ServiceName]++
			if se.SlaPerService[result.TeamID][result.ServiceName] >= conf.MiscSettings.SlaThreshold {
				sla := db.SLASchema{
					TeamID:      result.TeamID,
					ServiceName: result.ServiceName,
					RoundID:     uint(se.CurrentRound),
					Penalty:     *conf.MiscSettings.SlaPenalty,
				}
				if _, err := se.DB.CreateSLA(sla); err != nil {
					slog.Error("failed to create SLA", "team", result.TeamID, "service", result.ServiceName, "error", err)
				}
				se.SlaPerService[result.TeamID][result.ServiceName] = 0
			}
		}
	}
	se.uptimeMu.Unlock()

	slog.Debug("Successfully processed results for round", "round", se.CurrentRound, "total", len(dbResults))

	// Refresh materialized view asynchronously, but avoid concurrent refreshes
	currentRound := se.CurrentRound
	if se.Refreshing.CompareAndSwap(false, true) {
		go func(round uint) {
			defer se.Refreshing.Store(false)
			if err := se.DB.RefreshScoresMaterializedView(); err != nil {
				slog.Error("failed to refresh materialized view", "round", round, "error", err)
			}
		}(currentRound)
	} else {
		slog.Debug("refresh already in progress, skipping refresh spawn", "round", currentRound)
	}
}

func NewTestEngine(t *testing.T, redis *testutil.RedisContainer, pg *testutil.PostgresContainer, slaThreshold int) *ScoringEngine {
	t.Helper()

	slaPenalty := 30
	conf := &config.ConfigSettings{
		RequiredSettings: config.RequiredConfig{
			EventName:    "Test Event",
			EventType:    "rvb",
			DBConnectURL: "test", // Already connected via db.Connect
			BindAddress:  "127.0.0.1",
		},
		MiscSettings: config.MiscConfig{
			Delay:        5,
			Jitter:       1, // Must be non-zero to avoid rand.Intn(0) panic
			Timeout:      3,
			Points:       10,
			SlaThreshold: slaThreshold,
			SlaPenalty:   &slaPenalty,
		},
	}

	se := &ScoringEngine{
		Config:           config.NewLive(conf),
		credentialsMutex: make(map[uint]*sync.Mutex),
		UptimePerService: make(map[uint]map[string]db.Uptime),
		SlaPerService:    make(map[uint]map[string]int),
		RedisClient:      redis.Client,
		DB:               pg.DB,
		CurrentRound:     1,
	}
	se.resetPauseState()
	return se
}
