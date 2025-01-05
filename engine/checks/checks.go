package checks

import (
	"encoding/csv"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"
	"log/slog"
)

// checks for each service
type Runner interface {
	Run(teamID uint, identifier string, resultsChan chan Result)
	Runnable() bool
	Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error
	GetType() string
}

// services will inherit Service so that config.Config can be read from file, but will not be used after initial read
type Service struct {
	Name         string         `toml:"-"`          // Name is the box name plus the service (ex. lunar-dns)
	Display      string         `toml:",omitempty"` // Display is the name of the service (ex. dns)
	CredLists    []string       `toml:",omitempty"`
	Port         int            `toml:",omitzero"` // omitzero because custom checks might not specify port, and shouldn't be assigned 0
	Points       int            `toml:",omitempty"`
	Timeout      int            `toml:",omitempty"`
	SlaPenalty   int            `toml:",omitempty"`
	SlaThreshold int            `toml:",omitempty"`
	LaunchTime   time.Time      `toml:",omitempty"`
	StopTime     time.Time      `toml:",omitempty"`
	Disabled     bool           `toml:",omitempty"`
	Target       string         `toml:",omitempty"` // Target is the IP address or hostname for the box
	ServiceType  string         `toml:",omitempty"` // ServiceType is the name of the Runner that checks the service
}

type Result struct {
	ServiceName string `json:"name,omitempty"`
	Target      string `json:"target,omitempty"`
	TeamID      uint   `json:"teamid,omitempty"`
	Status      bool   `json:"status,omitempty"`
	Debug       string `json:"debug,omitempty"`
	Error       string `json:"error,omitempty"`
	Points      int    `json:"points,omitempty"`
	ServiceType string `json:"service_type,omitempty"`
}

func (service *Service) GetType() string {
	return service.ServiceType
}

func (service *Service) getCreds(teamID uint) (string, string, error) {
	// check if credlists are defined, if not return error
	if len(service.CredLists) == 0 {
		return "", "", errors.New("no credlists defined")
	}

	// pick which list to use
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	credListName := service.CredLists[rng.Intn(len(service.CredLists))]

	// get the credlist from the filesystem
	filePath := fmt.Sprintf("submissions/pcrs/%d/%s", teamID, credListName)
	file, err := os.Open(filePath)
	if err != nil {
		return "", "", err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return "", "", err
	}

	if len(records) == 0 || len(records[0]) < 2 {
		return "", "", errors.New("invalid credlist format")
	}

	randomIndex := rng.Intn(len(records))

	username := records[randomIndex][0]
	password := records[randomIndex][1]

	return username, password, nil
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
	for _, list := range service.CredLists {
		if !strings.HasSuffix(list, ".credlist") {
			return errors.New("check " + service.Name + " has invalid credlist names")
		}
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

func (service *Service) Run(teamID uint, teamIdentifier string, resultsChan chan Result, definition func(teamID uint, teamIdentifier string, checkResult Result, response chan Result)) {
	service.Target = strings.Replace(service.Target, "_", teamIdentifier, -1)
	

	checkResult := Result{
		TeamID:      teamID,
		ServiceName: service.Name,
		Target:      service.Target,
		Points:      service.Points,
		Status:      false,
		ServiceType: service.ServiceType,
	}

	slog.Debug("Running %d %s with target %s", teamID, service.Name, service.Target)
	response := make(chan Result)

	go definition(teamID, teamIdentifier, checkResult, response)

	select {
	// ok response
	case resp := <-response:
		resultsChan <- resp
		return
	// timeout
	case <-time.After(time.Duration(service.Timeout) * time.Second):
		checkResult.Error = "timeout"
		resultsChan <- checkResult
		return
	}
}
