package checks

import (
	"context"
	"fmt"
	"net"

	"github.com/mitchellh/go-vnc"
)

type Vnc struct {
	Service
}

func (c Vnc) Run(teamID uint, boxIp string, boxFQDN string, res chan Result) {
	// Configure the vnc client
	username, password := getCreds(teamID, c.CredLists)
	config := vnc.ClientConfig{
		Auth: []vnc.ClientAuth{
			&vnc.PasswordAuth{Password: password},
		},
	}

	// Dial the vnc server
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(context.TODO(), "tcp", fmt.Sprintf("%s:%d", boxIp, c.Port))
	if err != nil {
		res <- Result{
			Error: "connection to vnc server failed",
			Debug: err.Error() + " for creds " + username + ":" + password,
		}
		return
	}
	defer conn.Close()

	vncClient, err := vnc.Client(conn, &config)
	if err != nil {
		res <- Result{
			Error: "failed to log in to VNC server",
			Debug: err.Error() + " for creds " + username + ":" + password,
		}
		return
	}
	defer vncClient.Close()

	res <- Result{
		Status: true,
		Points: c.Points,
		Debug:  "creds " + username + ":" + password,
	}
}

func (c Vnc) GetService() Service {
	return c.Service
}
