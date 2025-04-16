package checks

import (
	"fmt"
	"time"

	"github.com/go-ping/ping"
)

type Ping struct {
	Service
	Count           int
	AllowPacketLoss bool
	Percent         int
}

func (c Ping) Run(teamID uint, teamIdentifier string, roundID uint, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {
		// Create pinger
		pinger, err := ping.NewPinger(c.Target)
		if err != nil {
			checkResult.Error = "ping creation failed"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		// Send ping
		pinger.Count = 1
		pinger.Timeout = 5 * time.Second
		pinger.SetPrivileged(true)
		err = pinger.Run()
		if err != nil {
			checkResult.Error = "ping failed"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		stats := pinger.Statistics()
		// Check packet loss instead of count
		if c.AllowPacketLoss {
			if stats.PacketLoss >= float64(c.Percent) {
				checkResult.Error = "not enough pings succeeded"
				checkResult.Debug = "ping failed: packet loss of " + fmt.Sprintf("%.0f", stats.PacketLoss) + "% higher than limit of " + fmt.Sprintf("%d", c.Percent) + "%"
				response <- checkResult
				return
			}
			// Check for failure
		} else if stats.PacketsRecv != c.Count {
			checkResult.Error = "not all pings succeeded"
			checkResult.Debug = "packet loss of " + fmt.Sprintf("%f", stats.PacketLoss)
			response <- checkResult
			return
		}

		checkResult.Status = true
		checkResult.Points = c.Points
		response <- checkResult
	}

	c.Service.Run(teamID, teamIdentifier, roundID, resultsChan, definition)
}

func (c *Ping) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if c.ServiceType == "" {
		c.ServiceType = "Ping"
	}
	if err := c.Service.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
		return err
	}
	if c.Display == "" {
		c.Display = "ping"
	}
	if c.Name == "" {
		c.Name = box + "-" + c.Display
	}
	if c.Count == 0 {
		c.Count = 1
	}

	return nil
}
