package checks

import (
	"github.com/knadh/go-pop3"
)

type Pop3 struct {
	Service
	Encrypted bool
}

func (c Pop3) Run(teamID uint, teamIdentifier string, target string, res chan Result) {
	// Create a dialer so we can set timeouts
	p := pop3.New(pop3.Opt{
		Host:       target,
		Port:       c.Port,
		TLSEnabled: c.Encrypted,
	})

	// Create a new connection. POP3 connections are stateful and should end
	// with a Quit() once the opreations are done.
	conn, err := p.NewConn()
	if err != nil {
		res <- Result{
			Error: "connection to server failed",
			Debug: err.Error(),
		}
		return
	}
	defer conn.Quit()

	// Authenticate.
	if !c.Anonymous {
		username, password := getCreds(teamID, c.CredLists)
		if err := conn.Auth(username, password); err != nil {
			res <- Result{
				Error: "login failed",
				Debug: "creds " + username + ":" + password + ", error: " + err.Error(),
			}
			return
		}

		_, _, err := conn.Stat()
		if err != nil {
			res <- Result{
				Error: "listing mailboxes failed",
				Debug: err.Error(),
			}
			return
		}
		res <- Result{
			Status: true,
			Points: c.Points,
			Debug:  "mailbox listed successfully with creds " + username + ":" + password,
		}
	}
	res <- Result{
		Status: true,
		Points: c.Points,
		Debug:  "smtp server responded to request (anonymous)",
	}
}

func (c Pop3) GetService() Service {
	return c.Service
}
