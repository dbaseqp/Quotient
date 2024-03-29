package checks

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/lib/pq"
)

var (
	Creds map[string]map[uint]map[string]string

	// Global list of all current CredentialSet
)

// services will inherit Service so that config can be read from file, but will not be used after initial read
type Service struct {
	Name         string         `toml:"-"`          // Name is the box name plus the service (ex. lunar-dns)
	Display      string         `toml:",omitempty"` // Display is the name of the service (ex. dns)
	CredLists    pq.StringArray `toml:",omitempty"`
	Port         int            `toml:",omitzero"` // omitzero because custom checks might not specify port, and shouldn't be assigned 0
	Anonymous    bool           `toml:",omitempty"`
	Points       int            `toml:",omitempty"`
	Timeout      int            `toml:",omitempty"`
	SlaPenalty   int            `toml:",omitempty"`
	SlaThreshold int            `toml:",omitempty"`
	LaunchTime   time.Time      `toml:",omitempty"`
	StopTime     time.Time      `toml:",omitempty"`
	Disabled     bool           `toml:",omitempty"`
	BoxName      string         `toml:"-"`
	BoxIP        string         `toml:"-"`
	BoxFQDN      string         `toml:"-"`
}

func getCreds(teamID uint, credLists []string) (string, string) {
	// pick which list to use
	if len(credLists) == 0 {
		return "", ""
	}
	credListName := credLists[rand.Intn(len(credLists))]

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	if _, ok := Creds[credListName]; !ok || len(Creds[credListName][teamID]) == 0 {
		return "", ""
	}

	random := rng.Intn(len(Creds[credListName][teamID]))
	count := 0
	for username, password := range Creds[credListName][teamID] {
		if count == random {
			return username, password
		}
		count++
	}
	return "", ""
}

type Result struct {
	ServiceName string `json:"name,omitempty"`
	BoxName     string `json:"box,omitempty"`
	IP          string `json:"ip,omitempty"`
	Status      bool   `json:"status,omitempty"`
	Debug       string `json:"debug,omitempty"`
	Error       string `json:"error,omitempty"`
	Points      int    `json:"points,omitempty"`
}

// checks for each service
type Runner interface {
	Run(uint, string, chan Result)
	GetService() Service
}

func Dispatch(teamID uint, teamIdentifier string, boxName string, boxIP string, boxFQDN string, runner Runner, resChan chan Result) {
	// make temporary channel to race against timeout
	res := make(chan Result)
	fullIP := strings.Replace(boxIP, "_", fmt.Sprint(teamIdentifier), 1)
	fullFQDN := strings.Replace(boxIP, "_", fmt.Sprint(boxFQDN), 1)
	timeout := time.Duration(runner.GetService().Timeout) * time.Second

	var target string
	if fullFQDN != "" {
		target = fullFQDN
	} else {
		target = fullIP
	}

	go runner.Run(teamID, target, res)
	var result Result
	select {
	case result = <-res:
	case <-time.After(timeout):
		result.Debug = "Target: " + target
		result.Error = "Timed out"
	}
	result.ServiceName = runner.GetService().Name
	result.IP = fullIP
	result.BoxName = boxName

	resChan <- result
}
