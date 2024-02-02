package checks

import (
	"strconv"
)

type Tcp struct {
	Service
}

func (c Tcp) Run(teamID uint, boxIp string, res chan Result, service Service) {
	err := tcpCheck(boxIp + ":" + strconv.Itoa(service.Port))
	if err != nil {
		res <- Result{
			Error: "connection error",
			Debug: err.Error(),
		}
		return
	}
	res <- Result{
		Status: true,
		Debug:  "responded to request",
	}
}
