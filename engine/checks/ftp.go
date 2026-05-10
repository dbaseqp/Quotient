package checks

import (
	"errors"
	"io"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
)

type Ftp struct {
	Service
	File []FtpFile
}

type FtpFile struct {
	Name  string
	Hash  string
	Regex string
}

func (c Ftp) Run(teamID uint, teamIdentifier string, roundID uint, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {
		conn, err := ftp.Dial(c.Target+":"+strconv.Itoa(c.Port), ftp.DialWithTimeout(time.Duration(c.Timeout)*time.Second))
		if err != nil {
			checkResult.Error = "ftp connection failed"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		defer conn.Quit()

		var username, password string
		if len(c.CredLists) == 0 {
			username = "anonymous"
			password = "anonymous"
		} else {
			username, password, err = c.getCreds(teamID)
			if err != nil {
				checkResult.Error = "error getting creds"
				checkResult.Debug = err.Error()
				response <- checkResult
				return
			}
		}
		err = conn.Login(username, password)
		if err != nil {
			checkResult.Error = "ftp login failed"
			checkResult.Debug = "creds used were " + username + ":" + password + " with error " + err.Error()
			response <- checkResult
			return
		}

		if len(c.File) > 0 {
			checkResult = RunSubchecks(c.File, c.CheckAll, checkResult, "creds used were "+username+":"+password, func(file FtpFile, res Result) Result {
				r, err := conn.Retr(file.Name)
				if err != nil {
					res.Error = "failed to retrieve file " + file.Name
					res.Status = false
					return res
				}
				defer func() {
				if err := r.Close(); err != nil {
					slog.Error("failed to close ftp reader", "error", err)
				}
			}()
				buf, err := io.ReadAll(r)
				if err != nil {
					res.Error = "failed to read ftp file"
					res.Debug = "tried to read " + file.Name
					res.Status = false
					return res
				}
				if file.Regex != "" {
					re, err := regexp.Compile(file.Regex)
					if err != nil {
						res.Error = "error compiling regex to match for ftp file"
						res.Debug = err.Error()
						res.Status = false
						return res
					}
					reFind := re.Find(buf)
					if reFind == nil {
						res.Error = "couldn't find regex in file"
						res.Debug = "couldn't find regex \"" + file.Regex + "\" for " + file.Name
						res.Status = false
						return res
					}
				} else if file.Hash != "" {
					fileHash, err := StringHash(string(buf))
					if err != nil {
						res.Error = "error calculating file hash"
						res.Debug = err.Error()
						res.Status = false
						return res
					} else if !strings.EqualFold(fileHash, file.Hash) {
						res.Error = "file hash did not match"
						res.Debug = "file hash " + fileHash + " did not match specified hash " + file.Hash
						res.Status = false
						return res
					}
				}
				res.Status = true
				return res
			})
			response <- checkResult
			return
		}

		checkResult.Status = true
		checkResult.Debug = "creds used were " + username + ":" + password
		response <- checkResult
	}

	c.Service.Run(teamID, teamIdentifier, roundID, resultsChan, definition)
}

func (c *Ftp) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if c.ServiceType == "" {
		c.ServiceType = "Ftp"
	}
	if err := c.Service.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
		return err
	}
	if c.Port == 0 {
		c.Port = 21
	}
	if c.Display == "" {
		c.Display = "ftp"
	}
	if c.Name == "" {
		c.Name = box + "-" + c.Display
	}
	for _, f := range c.File {
		if f.Regex != "" && f.Hash != "" {
			return errors.New("can't have both regex and hash for ftp file check")
		}
	}

	return nil
}
