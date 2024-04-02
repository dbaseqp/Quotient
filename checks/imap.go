package checks

import (
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

type Imap struct {
	Service
	Encrypted bool
}

func (c Imap) Run(teamID uint, teamIdentifier string, target string, res chan Result) {
	// Create a dialer so we can set timeouts
	dialer := net.Dialer{
		Timeout: time.Duration(c.Timeout) * time.Second,
	}

	// Defining these allow the if/else block below
	var cl *client.Client
	var err error

	// Connect to server with TLS or not
	if c.Encrypted {
		cl, err = client.DialWithDialerTLS(&dialer, fmt.Sprintf("%s:%d", target, c.Port), &tls.Config{})
	} else {
		cl, err = client.DialWithDialer(&dialer, fmt.Sprintf("%s:%d", target, c.Port))
	}
	if err != nil {
		res <- Result{
			Error: "connection to server failed",
			Debug: err.Error(),
		}
		return
	}
	defer cl.Close()

	if !c.Anonymous {
		username, password := getCreds(teamID, c.CredLists)
		// Set timeout for commands
		cl.Timeout = time.Duration(c.Timeout) * time.Second

		// Login
		err = cl.Login(username, password)
		if err != nil {
			res <- Result{
				Error: "login failed",
				Debug: "creds " + username + ":" + password + ", error: " + err.Error(),
			}
			return
		}
		defer cl.Logout()

		// List mailboxes
		mailboxes := make(chan *imap.MailboxInfo, 10)
		err = cl.List("", "*", mailboxes)
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

func (c Imap) GetService() Service {
	return c.Service
}
