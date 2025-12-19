package checks

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/mitchellh/go-vnc"
)

type Vnc struct {
	Service
}

func (c Vnc) Run(teamID uint, teamIdentifier string, roundID uint, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {

		// Configure the vnc client
		username, password, err := c.getCreds(teamID)
		if err != nil {
			checkResult.Error = "error getting creds"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		config := vnc.ClientConfig{
			Auth: []vnc.ClientAuth{
				&vnc.PasswordAuth{Password: password},
			},
		}

		// Dial the vnc server
		dialer := net.Dialer{}
		conn, err := dialer.DialContext(context.TODO(), "tcp", fmt.Sprintf("%s:%d", c.Target, c.Port))
		if err != nil {
			checkResult.Error = "connection to vnc server failed"
			checkResult.Debug = err.Error() + " for creds " + username + ":" + password
			response <- checkResult
			return
		}
		defer func() {
		if err := conn.Close(); err != nil {
			slog.Error("failed to close vnc connection", "error", err)
		}
	}()

		vncClient, err := vnc.Client(conn, &config)
		if err != nil {
			checkResult.Error = "failed to log in to VNC server"
			checkResult.Debug = err.Error() + " for creds " + username + ":" + password
			response <- checkResult
			return
		}
		defer func() {
		if err := vncClient.Close(); err != nil {
			slog.Error("failed to close vnc client", "error", err)
		}
	}()

		checkResult.Status = true
		checkResult.Debug = "creds " + username + ":" + password
		response <- checkResult
	}

	c.Service.Run(teamID, teamIdentifier, roundID, resultsChan, definition)
}

func (c *Vnc) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if c.ServiceType == "" {
		c.ServiceType = "Vnc"
	}
	if err := c.Service.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
		return err
	}
	if c.Display == "" {
		c.Display = "vnc"
	}
	if c.Name == "" {
		c.Name = box + "-" + c.Display
	}
	if c.Port == 0 {
		c.Port = 5900
	}

	return nil
}
