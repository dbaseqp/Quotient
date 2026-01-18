package checks

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"net/smtp"
	"time"
)

// generateRandomContent creates unpredictable email content to prevent
// SMTP check bypass by accepting only known static messages.
func generateRandomContent() (subject string, body string) {
	// Generate random bytes for subject and body
	subjectBytes := make([]byte, 8)
	bodyBytes := make([]byte, 32)

	// #nosec G404 -- non-crypto random for email content noise
	rand.Read(subjectBytes)
	// #nosec G404 -- non-crypto random for email content noise
	rand.Read(bodyBytes)

	subject = hex.EncodeToString(subjectBytes)
	body = hex.EncodeToString(bodyBytes)
	return
}

type Smtp struct {
	Service
	Encrypted   bool
	Domain      string
	RequireAuth bool
}

type unencryptedAuth struct {
	smtp.Auth
}

func (a unencryptedAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	s := *server
	s.TLS = true

	return a.Auth.Start(&s)
}

func (c Smtp) Run(teamID uint, teamIdentifier string, roundID uint, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {
		// Create a dialer
		dialer := net.Dialer{
			Timeout: time.Duration(c.Timeout) * time.Second,
		}

		// Generate random content to prevent bypass via static message acceptance
		subject, body := generateRandomContent()

		// ***********************************************
		// Set up custom auth for bypassing net/smtp protections
		username, password, err := c.getCreds(teamID)
		if err != nil {
			checkResult.Error = "error getting creds"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		toUser, _, err := c.getCreds(teamID)
		if err != nil {
			checkResult.Error = "error getting creds"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		auth := unencryptedAuth{smtp.PlainAuth("", username+c.Domain, password, c.Target)}
		// ***********************************************

		if c.Domain != "" {
			username = username + c.Domain
			toUser = toUser + c.Domain
		}

		// The good way to do auth
		// auth := smtp.PlainAuth("", d.Username, d.Password, d.Host)
		// Create TLS config
		tlsConfig := tls.Config{
			InsecureSkipVerify: true, // #nosec G402 -- competition services may use self-signed certs
		}

		// Declare these for the below if block
		var conn net.Conn

		if c.Encrypted {
			conn, err = tls.DialWithDialer(&dialer, "tcp", fmt.Sprintf("%s:%d", c.Target, c.Port), &tlsConfig)
		} else {
			conn, err = dialer.DialContext(context.TODO(), "tcp", fmt.Sprintf("%s:%d", c.Target, c.Port))
		}
		if err != nil {
			checkResult.Error = "connection to server failed"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		defer func() {
		if err := conn.Close(); err != nil {
			slog.Error("failed to close smtp connection", "error", err)
		}
	}()

		// Create smtp client
		sconn, err := smtp.NewClient(conn, c.Target)
		if err != nil {
			checkResult.Error = "smtp client creation failed"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		defer sconn.Quit()

		// Login
		if len(c.CredLists) > 0 {
			authSupported, _ := sconn.Extension("AUTH")
			if c.RequireAuth || authSupported {
				err = sconn.Auth(auth)
				if err != nil {
					checkResult.Error = "login failed for " + username + ":" + password
					checkResult.Debug = err.Error()
					response <- checkResult
					return
				}
			}
		}

		// Set the sender
		err = sconn.Mail(username)
		if err != nil {
			checkResult.Error = "setting sender failed"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		// Set the receiver
		err = sconn.Rcpt(toUser)
		if err != nil {
			checkResult.Error = "setting receiver failed"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		// Create email writer
		wc, err := sconn.Data()
		if err != nil {
			checkResult.Error = "creating email writer failed"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		defer func() {
			if err := wc.Close(); err != nil {
				slog.Error("failed to close smtp writer", "error", err)
			}
		}()

		message := fmt.Sprintf("Subject: %s\n\n%s\n\n", subject, body)

		// Write the message using Fprint to avoid treating the contents as a
		// format string.
		_, err = fmt.Fprint(wc, message)
		if err != nil {
			checkResult.Error = "writing message failed"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		checkResult.Status = true
		checkResult.Debug = "successfully wrote '" + message + "' to " + toUser + " from " + username
		response <- checkResult
	}

	c.Service.Run(teamID, teamIdentifier, roundID, resultsChan, definition)
}

func (c *Smtp) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if c.ServiceType == "" {
		c.ServiceType = "Smtp"
	}
	if err := c.Service.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
		return err
	}
	if c.Display == "" {
		c.Display = "smtp"
	}
	if c.Name == "" {
		c.Name = box + "-" + c.Display
	}
	if c.Port == 0 {
		c.Port = 25
	}

	return nil
}
