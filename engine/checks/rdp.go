package checks

import (
	"net"
	"strconv"
	"time"
	// why are there no good rdp libraries?
)

// Rdp represents a Remote Desktop Protocol (RDP) service check.
type Rdp struct {
	Service
}

// Run executes the RDP service check by attempting to establish a TCP connection
// to the target host and port within the specified timeout.
func (c Rdp) Run(teamID uint, teamIdentifier string, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {
		_, err := net.DialTimeout("tcp", c.Target+":"+strconv.Itoa(c.Port), time.Duration(c.Timeout)*time.Second)
		if err != nil {
			checkResult.Error = "connection error"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		checkResult.Status = true
		response <- checkResult
	}

	c.Service.Run(teamID, teamIdentifier, resultsChan, definition)
}

// Verify configures the RDP service with the provided parameters and ensures
// that default values are set for display name, service name, and port if not specified.
func (c *Rdp) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if err := c.Service.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
		return err
	}
	if c.Display == "" {
		c.Display = "rdp"
	}
	if c.Name == "" {
		c.Name = box + "-" + c.Display
	}
	if c.Port == 0 {
		c.Port = 3389
	}

	return nil
}
