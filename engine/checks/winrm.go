package checks

import (
	"bytes"
	"math/rand"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/masterzen/winrm"
)

type WinRM struct {
	Service
	Encrypted   bool
	BadAttempts int
	Command     []winCommandData
}

type winCommandData struct {
	UseRegex bool `toml:",omitempty"`
	Command  string
	Output   string
}

func (c WinRM) Run(teamID uint, teamIdentifier string, roundID uint, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {
		username, password, err := c.getCreds(teamID)
		if err != nil {
			checkResult.Error = "error getting creds"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		params := *winrm.DefaultParameters

		// Run bad attempts if specified
		for i := 0; i < c.BadAttempts; i++ {
			endpoint := winrm.NewEndpoint(c.Target, c.Port, c.Encrypted, true, nil, nil, nil, time.Duration(c.Timeout)*time.Second)
			winrm.NewClientWithParameters(endpoint, username, uuid.New().String(), &params)
		}

		// Log in to WinRM
		endpoint := winrm.NewEndpoint(c.Target, c.Port, c.Encrypted, true, nil, nil, nil, time.Duration(c.Timeout)*time.Second)
		client, err := winrm.NewClientWithParameters(endpoint, username, password, &params)
		if err != nil {
			checkResult.Error = "error creating winrm client"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		// If any commands specified, run them
		if len(c.Command) > 0 {
			r := c.Command[rand.Intn(len(c.Command))]
			powershellCmd := winrm.Powershell(r.Command)
			bufOut := new(bytes.Buffer)
			bufErr := new(bytes.Buffer)
			_, err = client.Run(powershellCmd, bufOut, bufErr)
			output := bufOut.Bytes()
			errString := bufErr.String()
			if err != nil {
				checkResult.Error = "failed with creds " + username + ":" + password
				checkResult.Debug = err.Error()
				response <- checkResult
				return
			} else if errString != "" {
				checkResult.Error = "command produced an error message"
				checkResult.Debug = "error: " + errString
				response <- checkResult
				return
			}
			if r.Output != "" {
				if r.UseRegex {
					re := regexp.MustCompile(r.Output)
					if !re.Match(output) {
						checkResult.Error = "command output didn't match regex"
						checkResult.Debug = "command output'" + r.Command + "' didn't match regex '" + r.Output
						response <- checkResult
						return
					}
				} else {
					if strings.TrimSpace(string(output)) != r.Output {
						checkResult.Error = "command output didn't match string"
						checkResult.Debug = "command output of '" + r.Command + "' didn't match string '" + r.Output
						response <- checkResult
						return
					}
				}
			}
		}
		checkResult.Status = true
		checkResult.Points = c.Points
		checkResult.Debug = "creds used were " + username + ":" + password
		response <- checkResult
	}

	c.Service.Run(teamID, teamIdentifier, roundID, resultsChan, definition)
}

func (c *WinRM) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if c.ServiceType == "" {
		c.ServiceType = "WinRM"
	}
	if err := c.Service.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
		return err
	}
	if c.Display == "" {
		c.Display = "winrm"
	}
	if c.Name == "" {
		c.Name = box + "-" + c.Display
	}
	if c.Port == 0 {
		if c.Encrypted {
			c.Port = 443
		} else {
			c.Port = 80
		}
	}
	for _, r := range c.Command {
		if r.UseRegex {
			regexp.MustCompile(r.Output)
		}
	}
	return nil
}
