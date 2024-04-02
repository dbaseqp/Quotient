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

func (c Ping) Run(teamID uint, teamIdentifier string, target string, res chan Result) {
	// Create pinger
	pinger, err := ping.NewPinger(target)
	if err != nil {
		res <- Result{
			Error: "ping creation failed",
			Debug: err.Error(),
		}
		return
	}

	// Send ping
	pinger.Count = 1
	pinger.Timeout = 5 * time.Second
	pinger.SetPrivileged(true)
	err = pinger.Run()
	if err != nil {
		res <- Result{
			Error: "ping failed",
			Debug: err.Error(),
		}
		return
	}

	stats := pinger.Statistics()
	// Check packet loss instead of count
	if c.AllowPacketLoss {
		if stats.PacketLoss >= float64(c.Percent) {
			res <- Result{
				Error: "not enough pings suceeded",
				Debug: "ping failed: packet loss of " + fmt.Sprintf("%.0f", stats.PacketLoss) + "% higher than limit of " + fmt.Sprintf("%d", c.Percent) + "%",
			}
			return
		}
		// Check for failure
	} else if stats.PacketsRecv != c.Count {
		res <- Result{
			Error: "not all pings suceeded",
			Debug: "packet loss of " + fmt.Sprintf("%f", stats.PacketLoss),
		}
		return
	}

	res <- Result{
		Status: true,
		Points: c.Points,
	}
}

func (c Ping) GetService() Service {
	return c.Service
}
