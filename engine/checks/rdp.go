package checks

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nakagami/grdp"
)

type Rdp struct {
	Service
	Domain string `toml:",omitempty"`
}

func (c Rdp) Run(teamID uint, teamIdentifier string, roundID uint, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {
		// If no credentials configured, just do a port check
		if len(c.CredLists) == 0 {
			_, err := net.DialTimeout("tcp", c.Target+":"+strconv.Itoa(c.Port), time.Duration(c.Timeout)*time.Second)
			if err != nil {
				checkResult.Error = "connection error"
				checkResult.Debug = err.Error()
				response <- checkResult
				return
			}
			checkResult.Status = true
			response <- checkResult
			return
		}

		// Get credentials and attempt RDP authentication
		username, password, err := c.getCreds(teamID)
		if err != nil {
			checkResult.Error = "error getting creds"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		// Parse domain from username if in DOMAIN\user or user@domain format
		domain := c.Domain
		if strings.Contains(username, "\\") {
			parts := strings.SplitN(username, "\\", 2)
			domain = parts[0]
			username = parts[1]
		} else if strings.Contains(username, "@") {
			parts := strings.SplitN(username, "@", 2)
			username = parts[0]
			domain = parts[1]
		}

		host := fmt.Sprintf("%s:%d", c.Target, c.Port)

		rdpClient := grdp.NewRdpClient(host, 800, 600)

		var loginErr error
		var loginSuccess bool
		var mu sync.Mutex
		done := make(chan struct{})

		rdpClient.OnError(func(err error) {
			mu.Lock()
			defer mu.Unlock()
			if loginErr == nil {
				loginErr = err
			}
			select {
			case <-done:
			default:
				close(done)
			}
		})

		rdpClient.OnSucces(func() { // Note: typo in library API
			mu.Lock()
			defer mu.Unlock()
			loginSuccess = true
			select {
			case <-done:
			default:
				close(done)
			}
		})

		go func() {
			if err := rdpClient.Login(domain, username, password); err != nil {
				mu.Lock()
				defer mu.Unlock()
				if loginErr == nil {
					loginErr = err
				}
				select {
				case <-done:
				default:
					close(done)
				}
			}
		}()

		select {
		case <-done:
		case <-time.After(time.Duration(c.Timeout) * time.Second):
			rdpClient.Close()
			checkResult.Error = "rdp connection timeout"
			checkResult.Debug = "connection timed out after " + strconv.Itoa(c.Timeout) + " seconds"
			response <- checkResult
			return
		}

		rdpClient.Close()

		mu.Lock()
		defer mu.Unlock()

		if loginSuccess {
			checkResult.Status = true
			checkResult.Debug = "creds used were " + username + ":" + password
			response <- checkResult
			return
		}

		if loginErr != nil {
			checkResult.Error = "rdp authentication failed for " + username + ":" + password
			checkResult.Debug = loginErr.Error()
			response <- checkResult
			return
		}

		checkResult.Error = "rdp connection failed"
		checkResult.Debug = "unknown error"
		response <- checkResult
	}

	c.Service.Run(teamID, teamIdentifier, roundID, resultsChan, definition)
}

func (c *Rdp) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if c.ServiceType == "" {
		c.ServiceType = "Rdp"
	}
	if err := c.Service.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
		return err
	}
	if c.Display == "" {
		c.Display = "rdp"
	}
	if c.Name == "" {
		c.Name = box + "-" + c.Display
	}
	if c.Port == 0 {
		c.Port = 3389
	}

	return nil
}
