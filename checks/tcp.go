package checks

import (
	"net"
	"strconv"
	"time"
)

type Tcp struct {
	Service
}

func (c Tcp) Run(teamID uint, teamIdentifier string, target string, res chan Result) {
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
		Debug:  "responded to request",
	}
}

func (c Tcp) GetService() Service {
	return c.Service
}
