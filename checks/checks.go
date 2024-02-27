package checks

import (
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"
)

var (
	GlobalTimeout time.Duration
	Creds         map[uint]map[string]map[string]string

	// Global list of all current CredentialSet
)

// wrapper to access data and methods (pseudo OOP)
type ServiceHandler struct {
	Service `toml:"generalconfigs"`
	Runner  `toml:"specificconfigs"`
	Type    string
}

// services will inherit Service so that config can be read from file, but will not be used after initial read
type Service struct {
	Name         string    `toml:",omitempty"` // Name is the box name plus the service (ex. lunar-dns)
	Display      string    `toml:",omitempty"` // Display is the name of the service (ex. dns)
	FQDN         string    `toml:"-"`
	IP           string    `toml:",omitempty"`
	CredLists    []string  `toml:",omitzero"`
	Port         int       `toml:",omitzero"`
	Anonymous    bool      `toml:",omitempty"`
	Points       int       `toml:",omitzero"`
	SlaPenalty   int       `toml:",omitzero"`
	SlaThreshold int       `toml:",omitzero"`
	LaunchTime   time.Time `toml:",omitempty"`
	StopTime     time.Time `toml:",omitempty"`
}

// checks for each service
type Runner interface {
	Run(uint, string, chan Result, Service)
}

type Result struct {
	Name   string `json:"name,omitempty"`
	Box    string `json:"box,omitempty"`
	Status bool   `json:"status,omitempty"`
	IP     string `json:"ip,omitempty"`
	Error  string `json:"error,omitempty"`
	Debug  string `json:"debug,omitempty"`
	Points int    `json:"points,omitempty"`
}

func getCreds(teamID uint, credLists []string) (string, string) {
	// pick which list to use
	if len(credLists) == 0 {
		return "", ""
	}
	credListName := credLists[rand.Intn(len(credLists))]

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	random := rng.Intn(len(Creds[teamID][credListName]))
	count := 0
	for username, password := range Creds[teamID][credListName] {
		if count == random {
			return username, password
		}
		count++
	}
	return "", ""
}

func RunCheck(teamID uint, teamIP int, boxIP string, boxName string, check ServiceHandler, timeout time.Duration, resChan chan Result) {
	// make temporary channel to race against timeout
	res := make(chan Result)
	result := Result{}
	fullIP := strings.Replace(boxIP, "x", fmt.Sprint(teamIP), 1)
	// go fake(teamID, fullIP, res, check.Service)
	go check.Run(teamID, fullIP, res, check.Service)
	select {
	case result = <-res:
	case <-time.After(timeout):
		result.Error = "Timed out"
	}
	result.Name = check.Name
	result.IP = fullIP
	result.Box = boxName
	if result.Status {
		result.Points = check.Service.Points
	}
	resChan <- result
}

func tcpCheck(hostIP string) error {
	_, err := net.DialTimeout("tcp", hostIP, GlobalTimeout)
	return err
}

/*
func percentChangedCreds() map[string]float {
	// get all usernames
	// for each team, see which % of creds exist in pcritems
}
*/
