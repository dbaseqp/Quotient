package checks

import (
	"bytes"
	"log/slog"
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
		params.TransportDecorator = func() winrm.Transporter {
			return &winrm.ClientNTLM{}
		}

		// Run bad attempts if specified
		for range c.BadAttempts {
			endpoint := winrm.NewEndpoint(c.Target, c.Port, c.Encrypted, true, nil, nil, nil, time.Duration(c.Timeout)*time.Second)
			if _, err := winrm.NewClientWithParameters(endpoint, username, uuid.New().String(), &params); err != nil {
				slog.Error("failed bad winrm attempt", "error", err)
			}
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

		// If any commands specified, run them; otherwise run a simple connectivity test
		var powershellCmd string
		if len(c.Command) > 0 {
			checkResult = RunSubchecks(c.Command, c.CheckAll, checkResult, "creds used were "+username+":"+password, func(r winCommandData, res Result) Result {
				powershellCmd = winrm.Powershell(r.Command)
				bufOut := new(bytes.Buffer)
				bufErr := new(bytes.Buffer)
				_, err = client.Run(powershellCmd, bufOut, bufErr)
				output := bufOut.Bytes()
				errString := bufErr.String()
				if err != nil {
					res.Error = "failed with creds " + username + ":" + password
					res.Debug = err.Error()
					res.Status = false
					return res
				} else if errString != "" {
					res.Error = "command produced an error message"
					res.Debug = "error: " + errString
					res.Status = false
					return res
				}
				if r.Output != "" {
					if r.UseRegex {
						re := regexp.MustCompile(r.Output)
						if !re.Match(output) {
							res.Error = "command output didn't match regex"
							res.Debug = "command output'" + r.Command + "' didn't match regex '" + r.Output
							res.Status = false
							return res
						}
					} else {
						if strings.TrimSpace(string(output)) != r.Output {
							res.Error = "command output didn't match string"
							res.Debug = "command output of '" + r.Command + "' didn't match string '" + r.Output
							res.Status = false
							return res
						}
					}
				}
				res.Status = true
				return res
			})
			if !checkResult.Status {
				response <- checkResult
				return
			}
		} else {
			powershellCmd = winrm.Powershell("hostname")
			bufOut := new(bytes.Buffer)
			bufErr := new(bytes.Buffer)
			_, err = client.Run(powershellCmd, bufOut, bufErr)
			if err != nil {
				checkResult.Error = "connection test failed with creds " + username + ":" + password
				checkResult.Debug = err.Error()
				response <- checkResult
				return
			}
			checkResult.Status = true
			checkResult.Debug = "creds used were " + username + ":" + password
		}
		
		checkResult.Points = c.Points
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
