package checks

import (
	"errors"
	"strconv"
	"time"
)

type Tcp struct {
	Service
}

func (c Tcp) Run(teamID uint, teamIdentifier string, roundID uint, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {
		if err := dialAndClose(c.Service); err != nil {
			checkResult.Error = "connection error"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		checkResult.Status = true
		checkResult.Debug = "responded to request"
		response <- checkResult
	}

	c.Service.Run(teamID, teamIdentifier, roundID, resultsChan, definition)
}

// dialAndClose opens a TCP connection to the service's target and port and closes it straight
// away, without sending anything. The close is a FIN, or an RST if the server has already sent
// data.
func dialAndClose(s Service) error {
	conn, err := clientLinux.dialer(time.Duration(s.Timeout)*time.Second).Dial("tcp", s.Target+":"+strconv.Itoa(s.Port))
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

func (c *Tcp) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if c.ServiceType == "" {
		c.ServiceType = "Tcp"
	}
	if err := c.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
		return err
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
