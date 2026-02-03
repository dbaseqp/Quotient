package checks

import (
	"errors"
	"log/slog"
	"math/rand"
	"strings"
	"time"
)

// TaskCredential represents a credential from the task payload
type TaskCredential struct {
	Username string
	Password string
}

// checks for each service
type Runner interface {
	Run(teamID uint, identifier string, roundID uint, resultsChan chan Result)
	Runnable() bool
	Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error
	GetType() string
	GetName() string
	GetAttempts() int
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

func (service *Service) getCreds(teamID uint) (string, string, error) {
	if len(service.TaskCredentials) == 0 {
		return "", "", errors.New("no credentials available")
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano())) // #nosec G404 -- non-crypto RNG
	randomIndex := rng.Intn(len(service.TaskCredentials))
	return service.TaskCredentials[randomIndex].Username, service.TaskCredentials[randomIndex].Password, nil
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
	service.Target = strings.Replace(service.Target, "_", teamIdentifier, -1)

	checkResult := Result{
		TeamID:      teamID,
		ServiceName: service.Name,
		Target:      service.Target,
		Points:      service.Points,
		Status:      false,
		ServiceType: service.ServiceType,
		RoundID:     roundID,
	}

	slog.Debug("Running check", "teamID", teamID, "serviceName", service.Name, "target", service.Target)
	response := make(chan Result)

	go definition(teamID, teamIdentifier, checkResult, response)

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
