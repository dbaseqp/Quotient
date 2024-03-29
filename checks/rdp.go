package checks

import (
	"net"
	"strconv"
	"time"
	// why are there no good rdp libraries?
)

type Rdp struct {
	Service
}

func (c Rdp) Run(teamID uint, target string, res chan Result) {
	_, err := net.DialTimeout("tcp", target+":"+strconv.Itoa(c.Port), time.Duration(c.Timeout)*time.Second)
	if err != nil {
		res <- Result{
			Error: "connection error",
			Debug: err.Error(),
		}
		return
	}
	res <- Result{
		Status: true,
		Points: c.Points,
		Debug:  "responded",
	}
}

func (c Rdp) GetService() Service {
	return c.Service
}
