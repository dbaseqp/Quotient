package checks

import (
	"fmt"

	"github.com/knadh/go-pop3"
)

type Pop3 struct {
	Service
	Encrypted bool
}

func (c Pop3) Run(teamID uint, boxIp string, res chan Result, service Service) {
	// Create a dialer so we can set timeouts
	p := pop3.New(pop3.Opt{
		Host:       boxIp,
		Port:       service.Port,
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
	if !service.Anonymous {
		username, password := getCreds(teamID, service.CredLists)
		if err := conn.Auth(username, password); err != nil {
			res <- Result{
				Error: "login failed",
				Debug: "creds " + username + ":" + password + ", error: " + err.Error(),
			}
			return
		}

		// Print the total number of messages and their size.
		count, size, _ := conn.Stat()
		fmt.Println("total messages=", count, "size=", size)
		if err != nil {
			res <- Result{
				Error: "listing mailboxes failed",
				Debug: err.Error(),
			}
			return
		}
		res <- Result{
			Status: true,
			Debug:  "mailbox listed successfully with creds " + username + ":" + password,
		}
	}
	res <- Result{
		Status: true,
		Debug:  "smtp server responded to request (anonymous)",
	}
}
