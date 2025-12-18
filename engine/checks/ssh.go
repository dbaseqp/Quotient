package checks

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
)

type Ssh struct {
	Service
	PrivKey     string `toml:",omitempty"`
	BadAttempts int    `toml:",omitzero"`
	Command     []commandData
}

type commandData struct {
	UseRegex bool
	Contains bool
	Command  string `toml:",omitempty"`
	Output   string `toml:",omitempty"`
}

func (c Ssh) Run(teamID uint, teamIdentifier string, roundID uint, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {

		// Create client config
		username, password, err := c.getCreds(teamID)
		if err != nil {
			checkResult.Error = "error getting creds"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		config := &ssh.ClientConfig{
			User:            username,
			HostKeyCallback: ssh.InsecureIgnoreHostKey(), // #nosec G106 -- competition hosts have unknown keys
			Timeout:         time.Duration(c.Timeout) * time.Second,
		}
		config.SetDefaults()
		config.Ciphers = append(config.Ciphers, "3des-cbc")
		if c.PrivKey != "" {
			key, err := os.ReadFile("./config/scoredfiles/" + c.PrivKey)
			if err != nil {
				checkResult.Error = "error opening pubkey"
				checkResult.Debug = err.Error()
				response <- checkResult
				return
			}
			signer, err := ssh.ParsePrivateKey(key)
			if err != nil {
				checkResult.Error = "error parsing private key"
				checkResult.Debug = err.Error()
				response <- checkResult
				return
			}
			config.Auth = []ssh.AuthMethod{
				ssh.PublicKeys(signer),
			}
		} else {
			config.Auth = []ssh.AuthMethod{
				ssh.Password(password),
			}
		}

		for range c.BadAttempts {
			badConf := &ssh.ClientConfig{
				User: username,
				Auth: []ssh.AuthMethod{
					ssh.Password(uuid.New().String()),
				},
				HostKeyCallback: ssh.InsecureIgnoreHostKey(), // #nosec G106 -- competition hosts have unknown keys
				Timeout:         time.Duration(c.Timeout) * time.Second,
			}

			badConn, err := ssh.Dial("tcp", c.Target+":"+strconv.Itoa(c.Port), badConf)
			if err == nil {
				if err := badConn.Close(); err != nil {
					slog.Error("failed to close bad ssh connection", "error", err)
				}
			}
		}

		// Connect to ssh server
		conn, err := ssh.Dial("tcp", c.Target+":"+strconv.Itoa(c.Port), config)
		if err != nil {
			if c.PrivKey != "" {
				checkResult.Error = "error logging in to ssh server with private key " + c.PrivKey
				checkResult.Debug = "error: " + err.Error()
			} else {
				checkResult.Error = "error logging in to ssh server for creds " + username + ":" + password
				checkResult.Debug = "error: " + err.Error()
			}
			response <- checkResult
			return
		}
		defer func() {
		if err := conn.Close(); err != nil {
			slog.Error("failed to close ssh connection", "error", err)
		}
	}()

		// Create a session
		session, err := conn.NewSession()
		if err != nil {
			checkResult.Error = "unable to create ssh session"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		defer session.Close()

		// Set up terminal modes
		modes := ssh.TerminalModes{
			ssh.ECHO:          0,     // disable echoing
			ssh.TTY_OP_ISPEED: 14400, // input speed = 14.4kbaud
			ssh.TTY_OP_OSPEED: 14400, // output speed = 14.4kbaud
		}

		// Request pseudo terminal
		if err := session.RequestPty("xterm", 40, 80, modes); err != nil {
			checkResult.Error = "couldn't allocate pts"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		// I/O for shell
		stdin, err := session.StdinPipe()
		if err != nil {
			checkResult.Error = "couldn't get stdin pipe"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		var stdoutBytes bytes.Buffer
		var stderrBytes bytes.Buffer
		session.Stdout = &stdoutBytes
		session.Stderr = &stderrBytes

		// Start remote shell
		if err := session.Shell(); err != nil {
			checkResult.Error = "failed to start shell"
			checkResult.Debug = "error: " + err.Error()
			response <- checkResult
			return
		}

		// If any commands specified, run a random one
		if len(c.Command) > 0 {
			r := c.Command[rand.Intn(len(c.Command))] // #nosec G404 -- non-crypto selection of command to test
			fmt.Fprintln(stdin, r.Command)
			time.Sleep(time.Duration(int(time.Duration(c.Timeout)*time.Second) / 8)) // command wait time
			if r.Contains {
				if !strings.Contains(stdoutBytes.String(), r.Output) {
					checkResult.Error = "command output didn't contain string"
					checkResult.Debug = "command output of '" + r.Command + "' didn't contain string '" + r.Output + "': " + stdoutBytes.String() + ",  " + stderrBytes.String()
					response <- checkResult
					return
				}
			} else if r.UseRegex {
				re := regexp.MustCompile(r.Output)
				if !re.Match(stdoutBytes.Bytes()) {
					checkResult.Error = "command output didn't match regex"
					checkResult.Debug = "command output'" + r.Command + "' didn't match regex '" + r.Output
					response <- checkResult
					return
				}
			} else {
				if stderrBytes.Len() != 0 {
					checkResult.Error = "command returned an error"
					checkResult.Debug = "command stderr was not empty: " + stderrBytes.String()
					response <- checkResult
					return
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

func (c *Ssh) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if c.ServiceType == "" {
		c.ServiceType = "Ssh"
	}
	if err := c.Service.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
		return err
	}
	if c.Display == "" {
		c.Display = "ssh"
	}
	if c.Name == "" {
		c.Name = box + "-" + c.Display
	}
	if c.Port == 0 {
		c.Port = 22
	}
	if c.PrivKey != "" && c.BadAttempts != 0 {
		return errors.New("cannot use both private key and bad attempts")
	}
	for _, r := range c.Command {
		if r.UseRegex {
			regexp.MustCompile(r.Output)
		}
		if r.UseRegex {
			regexp.MustCompile(r.Output)
		}
	}

	return nil
}
