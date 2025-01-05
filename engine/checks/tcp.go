package checks

import (
	"errors"
	"net"
	"strconv"
	"time"
)

type Tcp struct {
	Service
}

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

func (c *Tcp) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if c.ServiceType == "" {
		c.ServiceType = "Tcp"
	}
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
