package checks

import (
	"strconv"
	// why are there no good rdp libraries?
)

type Rdp struct {
	Service
}

func (c Rdp) Run(teamID uint, boxIp string, res chan Result, service Service) {
	err := tcpCheck(boxIp + ":" + strconv.Itoa(c.Port))
	if err != nil {
		res <- Result{
			Error: "connection error",
			Debug: err.Error(),
		}
		return
	}
	res <- Result{
		Status: true,
		Debug:  "responded",
	}
}
