package checks

import (
	"io"
	"log/slog"
	"net"
	"regexp"
	"strconv"

	"github.com/hirochachacha/go-smb2"
)

type Smb struct {
	Service
	Domain string
	Share  string
	File   []smbFile
}

type smbFile struct {
	Name  string
	Hash  string
	Regex string
}

func (c Smb) Run(teamID uint, teamIdentifier string, roundID uint, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {
		var username, password string
		if len(c.CredLists) == 0 {
			username, password = "guest", ""
		} else {
			var err error
			username, password, err = c.getCreds(teamID)
			if err != nil {
				checkResult.Error = "error getting creds"
				checkResult.Debug = err.Error()
				response <- checkResult
				return
			}
		}

		conn, err := net.Dial("tcp", c.Target+":"+strconv.Itoa(c.Port))
		if err != nil {
			checkResult.Error = "smb connection failed"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		defer func() {
		if err := conn.Close(); err != nil {
			slog.Error("failed to close smb connection", "error", err)
		}
	}()

		d := &smb2.Dialer{
			Initiator: &smb2.NTLMInitiator{
				User:     username,
				Password: password,
			},
		}

		s, err := d.Dial(conn)
		if err != nil {
			checkResult.Error = "smb login failed"
			if len(c.CredLists) == 0 {
				checkResult.Debug = err.Error()
			} else {
				checkResult.Debug = "error: " + err.Error() + ", creds " + username + ":" + password
			}
			response <- checkResult
			return
		}
		defer s.Logoff()

		if len(c.File) > 0 {
			fs, err := s.Mount(c.Share)
			if err != nil {
				checkResult.Error = "failed to mount share"
				checkResult.Debug = "share " + c.Share + ", creds " + username + ":" + password
				response <- checkResult
				return
			}
			defer fs.Umount()

			checkResult = RunSubchecks(c.File, c.CheckAll, checkResult, "creds used were "+username+":"+password, func(file smbFile, res Result) Result {
				f, err := fs.Open(file.Name)
				if err != nil {
					res.Error = "failed to open file"
					res.Debug = "file was " + file.Name + " (" + err.Error() + ")"
					res.Status = false
					return res
				}
				defer func() {
				if err := f.Close(); err != nil {
					slog.Error("failed to close smb file", "error", err)
				}
			}()

				buf, err := io.ReadAll(f)
				if err != nil {
					res.Error = "failed to read file"
					res.Debug = "file was " + file.Name + " (" + err.Error() + ")"
					res.Status = false
					return res
				}

				if file.Regex != "" {
					re, err := regexp.Compile(file.Regex)
					if err != nil {
						res.Error = "error compiling regex to match for smb file"
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
					res.Status = true
					res.Debug = "smb file " + file.Name + " matched regex"
					return res
				} else if file.Hash != "" {
					fileHash, err := StringHash(string(buf))
					if err != nil {
						res.Error = "error calculating file hash"
						res.Debug = "file " + file.Name + ", " + err.Error()
						res.Status = false
						return res
					} else if fileHash != file.Hash {
						res.Error = "file hash did not match"
						res.Debug = "file " + file.Name + " hash " + fileHash + " did not match specified hash " + file.Hash
						res.Status = false
						return res
					}

					res.Status = true
					res.Debug = "smb file " + file.Name + " matched hash file"
					return res
				} else {
					res.Status = true
					res.Debug = "smb file " + file.Name + " retrieval successful"
					return res
				}
			})
			response <- checkResult
			return
		} else {
			checkResult.Status = true
			checkResult.Debug = "smb login succeeded, creds " + username + ":" + password
			response <- checkResult
			return
		}
	}

	c.Service.Run(teamID, teamIdentifier, roundID, resultsChan, definition)
}

func (c *Smb) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if c.ServiceType == "" {
		c.ServiceType = "Smb"
	}
	if err := c.Service.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
		return err
	}
	if c.Display == "" {
		c.Display = "smb"
	}
	if c.Name == "" {
		c.Name = box + "-" + c.Display
	}
	if c.Port == 0 {
		c.Port = 445
	}

	return nil
}
