package checks

import (
	"time"

	"github.com/knadh/go-pop3"
)

type Pop3 struct {
	Service
	Domain    string
	Encrypted bool
}

func (c Pop3) Run(teamID uint, teamIdentifier string, roundID uint, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {
		// Create a dialer so we can set timeouts
		p := pop3.New(pop3.Opt{
			Host:       c.Target,
			Port:       c.Port,
			TLSEnabled: c.Encrypted,
			Dialer:     clientLinux.dialer(time.Duration(c.Timeout) * time.Second),
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
		// nolint:errcheck
		defer conn.Quit()

		// Ask for capabilities first, as mail clients do (curl, Thunderbird). The answer is not used:
		// a pre-RFC 2449 server says -ERR and a broken one fails at the next command, as before.
		// nolint:errcheck
		conn.Cmd("CAPA", true)

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
			// USER then PASS. go-pop3's Auth also sends a NOOP after PASS, which mail clients do not;
			// a server that only rejects the login at the next command now fails at STAT instead.
			if err := pop3Login(conn, username, password); err != nil {
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

	c.Service.Run(teamID, teamIdentifier, roundID, resultsChan, definition)
}

func (c *Pop3) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if c.ServiceType == "" {
		c.ServiceType = "Pop3"
	}
	if err := c.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
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

// pop3Login sends USER and PASS the way mail clients do.
func pop3Login(conn *pop3.Conn, username, password string) error {
	if err := conn.User(username); err != nil {
		return err
	}
	return conn.Pass(password)
}
