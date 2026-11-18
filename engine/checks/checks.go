package checks

import (
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strings"
	"time"
)

// TaskCredential represents a credential from the task payload
type TaskCredential struct {
	Username string
	Password string
	Credlist string
}

// checks for each service
type Runner interface {
	Run(teamID uint, identifier string, roundID uint, resultsChan chan Result)
	Runnable() bool
	Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error
	GetType() string
	GetName() string
	GetAttempts() int
	GetAttemptDelay() int
	GetTimeout() int
	GetPoints() int
	GetCredlists() []string
	SetTaskCredentials(creds []TaskCredential)
}

// services will inherit Service so that config.Config can be read from file, but will not be used after initial read
type Service struct {
	Name            string           `toml:"-"`          // Name is the box name plus the service (ex. lunar-dns)
	Display         string           `toml:",omitempty"` // Display is the name of the service (ex. dns)
	CredLists       []string         `toml:",omitempty"`
	Port            int              `toml:",omitzero"` // omitzero because custom checks might not specify port, and shouldn't be assigned 0
	Points          int              `toml:",omitempty"`
	Timeout         int              `toml:",omitempty"`
	SlaPenalty      int              `toml:",omitempty"`
	SlaThreshold    int              `toml:",omitempty"`
	LaunchTime      time.Time        `toml:",omitempty"`
	StopTime        time.Time        `toml:",omitempty"`
	Disabled        bool             `toml:",omitempty"`
	Target          string           `toml:",omitempty"` // Target is the IP address or hostname for the box
	ServiceType     string           `toml:",omitempty"` // ServiceType is the name of the Runner that checks the service
	Attempts        int              `toml:",omitempty"` // Attempts is the number of times the service has been checked
	AttemptDelay    int              `toml:",omitempty"` // AttemptDelay is the number of seconds to wait before retrying a failed attempt
	TaskCredentials []TaskCredential `toml:"-"`          // Credentials from task payload (set per-task, not from config)
}

type Result struct {
	ServiceName string `json:"name,omitempty"`
	Target      string `json:"target,omitempty"`
	TeamID      uint   `json:"team_id,omitempty"`
	Status      bool   `json:"status,omitempty"`
	Debug       string `json:"debug,omitempty"`
	Error       string `json:"error,omitempty"`
	Points      int    `json:"points,omitempty"`
	ServiceType string `json:"service_type,omitempty"`
	RoundID     uint   `json:"round_id"`
	Preflight   bool   `json:"preflight,omitempty"`

	// Added for runner visualization
	RunnerID   string `json:"runner_id,omitempty"`
	StartTime  string `json:"start_time,omitempty"`
	EndTime    string `json:"end_time,omitempty"`
	StatusText string `json:"status_text,omitempty"` // "running", "success", or "failed"
}

func (service *Service) GetType() string {
	return service.ServiceType
}

func (service *Service) GetName() string {
	return service.Name
}

func (service *Service) GetAttempts() int {
	return service.Attempts
}

func (service *Service) GetAttemptDelay() int {
	return service.AttemptDelay
}

func (service *Service) GetTimeout() int {
	return service.Timeout
}

func (service *Service) GetPoints() int {
	return service.Points
}

// SetAttemptDefaults fills Attempts and AttemptDelay from the event-wide
// defaults when the service doesn't set them.
func (service *Service) SetAttemptDefaults(attempts int, attemptDelay int) {
	if service.Attempts == 0 {
		service.Attempts = attempts
	}
	if service.AttemptDelay == 0 {
		service.AttemptDelay = attemptDelay
	}
}

func (service *Service) GetCredlists() []string {
	return service.CredLists
}

func (service *Service) SetCredlists(lists []string) {
	service.CredLists = lists
}

// SetTaskCredentials sets credentials from the task payload (thread-safe per-instance)
func (service *Service) SetTaskCredentials(creds []TaskCredential) {
	service.TaskCredentials = creds
}

// getCreds picks a random credential, leaving out rows from the credlists in exclude.
func (service *Service) getCreds(teamID uint, exclude ...string) (string, string, error) {
	pool := make([]TaskCredential, 0, len(service.TaskCredentials))
	for _, cred := range service.TaskCredentials {
		if !slices.Contains(exclude, cred.Credlist) {
			pool = append(pool, cred)
		}
	}
	if len(pool) == 0 {
		return "", "", errors.New("no credentials available")
	}

	picked := pool[rand.IntN(len(pool))] // #nosec G404 -- non-crypto selection of a credential
	return picked.Username, picked.Password, nil
}

// getCredlistPassword returns the password of a random row of the named credlist.
func (service *Service) getCredlistPassword(name string) (string, error) {
	var rows []TaskCredential
	for _, cred := range service.TaskCredentials {
		if cred.Credlist == name {
			rows = append(rows, cred)
		}
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("no credentials available in credlist %q", name)
	}

	return rows[rand.IntN(len(rows))].Password, nil // #nosec G404 -- non-crypto selection of a credential
}

func (service *Service) Configure(ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	// Set defaults if they're unset for a service
	if service.Target == "" {
		service.Target = ip
	}
	if service.Points == 0 {
		service.Points = points
	}
	if service.Timeout == 0 {
		service.Timeout = timeout
	}
	if service.SlaPenalty == 0 {
		service.SlaPenalty = slapenalty
	}
	if service.SlaThreshold == 0 {
		service.SlaThreshold = slathreshold
	}
	if service.Attempts == 0 {
		service.Attempts = 1
	}

	return nil
}

func (service *Service) Runnable() bool {
	if service.Disabled {
		return false
	}
	if service.LaunchTime.After(time.Now()) {
		return false
	}
	if !service.StopTime.IsZero() && service.StopTime.Before(time.Now()) {
		return false
	}
	return true
}

func (service *Service) Run(teamID uint, teamIdentifier string, roundID uint, resultsChan chan Result, definition func(teamID uint, teamIdentifier string, checkResult Result, response chan Result)) {
	service.Target = strings.ReplaceAll(service.Target, "_", teamIdentifier)

	checkResult := Result{
		TeamID:      teamID,
		ServiceName: service.Name,
		Target:      service.Target,
		Status:      false,
		ServiceType: service.ServiceType,
		RoundID:     roundID,
	}

	slog.Debug("Running check", "teamID", teamID, "serviceName", service.Name, "target", service.Target)
	response := make(chan Result, 1)

	// A check library that panics on a malformed reply from a team's server
	// must fail that check, not kill the runner and every task it holds.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("check panicked", "service", service.Name, "team", teamID, "panic", r)
				failed := checkResult
				failed.Error = "check crashed on the service's response"
				failed.Debug = fmt.Sprint(r)
				select {
				case response <- failed:
				default:
				}
			}
		}()
		definition(teamID, teamIdentifier, checkResult, response)
	}()

	select {
	// ok response
	case resp := <-response:
		resultsChan <- resp
		return
	// timeout
	case <-time.After(time.Duration(service.Timeout) * time.Second):
		checkResult.Error = "check timeout exceeded"
		resultsChan <- checkResult
		return
	}
}
