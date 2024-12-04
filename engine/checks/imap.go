package checks

import (
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

// Imap represents an IMAP service check configuration.
type Imap struct {
	Service
	Domain    string
	Encrypted bool
}

// Run executes the IMAP service check for the given team and sends the result to the results channel.
func (c Imap) Run(teamID uint, teamIdentifier string, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {
		// Create a dialer so we can set timeouts
		dialer := net.Dialer{
			Timeout: time.Duration(c.Timeout) * time.Second,
		}

		// Defining these allow the if/else block below
		var cl *client.Client
		var err error

		// Connect to server with TLS or not
		if c.Encrypted {
			cl, err = client.DialWithDialerTLS(&dialer, fmt.Sprintf("%s:%d", c.Target, c.Port), &tls.Config{})
		} else {
			cl, err = client.DialWithDialer(&dialer, fmt.Sprintf("%s:%d", c.Target, c.Port))
		}
		if err != nil {
			checkResult.Error = "connection to server failed"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		defer cl.Close()

		if len(c.CredLists) > 0 {
			username, password, err := c.getCreds(teamID)
			if err != nil {
				checkResult.Error = "error getting creds"
				checkResult.Debug = err.Error()
				response <- checkResult
				return
			}

			// Set timeout for commands
			cl.Timeout = time.Duration(c.Timeout) * time.Second
			if c.Domain != "" {
				username = username + c.Domain
			}

			// Login
			err = cl.Login(username, password)
			if err != nil {
				checkResult.Error = "login failed"
				checkResult.Debug = "creds " + username + ":" + password + ", error: " + err.Error()
				response <- checkResult
				return
			}
			defer cl.Logout()

			// List mailboxes
			mailboxes := make(chan *imap.MailboxInfo, 10)
			err = cl.List("", "*", mailboxes)
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

// Verify configures the IMAP service check with the provided parameters.
// It sets the IP, points, timeout, SLA penalty, and SLA threshold.
// Additionally, it ensures default values for the port, display name, and service name.
func (c *Imap) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if err := c.Service.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
		return err
	}
	if c.Port == 0 {
		c.Port = 143
	}
	if c.Display == "" {
		c.Display = "imap"
	}
	if c.Name == "" {
		c.Name = box + "-" + c.Display
	}

	return nil
}
