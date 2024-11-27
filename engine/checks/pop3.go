package checks

import (
	"github.com/knadh/go-pop3"
)

type Pop3 struct {
	Service
	Domain    string
	Encrypted bool
}

func (c Pop3) Run(teamID uint, teamIdentifier string, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {
		// Create a dialer so we can set timeouts
		p := pop3.New(pop3.Opt{
			Host:       c.Target,
			Port:       c.Port,
			TLSEnabled: c.Encrypted,
		})

		// Create a new connection. POP3 connections are stateful and should end
		// with a Quit() once the opreations are done.
		conn, err := p.NewConn()
		if err != nil {
			checkResult.Error = "connection to server failed"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		defer conn.Quit()

		// Authenticate.
		if len(c.CredLists) > 0 {
			username, password, err := c.getCreds(teamID)
			if err != nil {
				checkResult.Error = "error getting creds"
				checkResult.Debug = err.Error()
				response <- checkResult
				return
			}

			if c.Domain != "" {
				username = username + c.Domain
			}
			if err := conn.Auth(username, password); err != nil {
				checkResult.Error = "login failed"
				checkResult.Debug = "creds " + username + ":" + password + ", error: " + err.Error()
				response <- checkResult
				return
			}

			_, _, err = conn.Stat()
			if err != nil {
				checkResult.Error = "listing mailboxes failed"
				checkResult.Debug = err.Error()
				response <- checkResult
				return
			}
			checkResult.Status = true
			checkResult.Debug = "mailbox listed successfully with creds " + username + ":" + password
			response <- checkResult
			return
		}

		checkResult.Status = true
		checkResult.Debug = "smtp server responded to request (anonymous)"
		response <- checkResult
	}

	c.Service.Run(teamID, teamIdentifier, resultsChan, definition)
}

func (c *Pop3) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if err := c.Service.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
		return err
	}
	if c.Display == "" {
		c.Display = "pop3"
	}
	if c.Name == "" {
		c.Name = box + "-" + c.Display
	}
	if c.Port == 0 {
		c.Port = 110
	}

	return nil
}
