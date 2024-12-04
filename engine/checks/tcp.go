package checks

import (
	"errors"
	"net"
	"strconv"
	"time"
)

// Tcp represents a TCP service check.
type Tcp struct {
	Service
}

// Run executes the TCP service check by attempting to establish a connection
// to the specified target and port within the given timeout.
func (c Tcp) Run(teamID uint, teamIdentifier string, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {
		_, err := net.DialTimeout("tcp", c.Target+":"+strconv.Itoa(c.Port), time.Duration(c.Timeout)*time.Second)
		if err != nil {
			checkResult.Error = "connection error"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		checkResult.Status = true
		checkResult.Debug = "responded to request"
		response <- checkResult
	}

	c.Service.Run(teamID, teamIdentifier, resultsChan, definition)
}

// Verify checks the configuration of the Tcp service and ensures all required fields are set.
func (c *Tcp) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if c.Display == "" {
		c.Display = "tcp"
	}
	if c.Name == "" {
		c.Name = box + "-" + c.Display
	}
	if c.Port == 0 {
		return errors.New("port is required")
	}

	return nil
}
