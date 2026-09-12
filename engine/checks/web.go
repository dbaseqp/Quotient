package checks

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/corpix/uarand"
)

type Web struct {
	Service
	Url    []urlData
	Scheme string
}

type urlData struct {
	Path        string
	Status      int    `toml:",omitempty"`
	Diff        int    `toml:",omitempty"`
	Regex       string `toml:",omitempty"`
	CompareFile string `toml:",omitempty"` // TODO implement
}

func (c Web) Run(teamID uint, teamIdentifier string, roundID uint, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {
		response <- RunSubchecks(c.Url, c.CheckAll, checkResult, "", func(u urlData, res Result) Result {
			// random user agent
			ua := uarand.GetRandom()

			tr := &http.Transport{
				MaxIdleConns:      1,
				IdleConnTimeout:   time.Duration(c.Timeout) * time.Second, // address this
				DisableKeepAlives: true,
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true, // #nosec G402 -- competition services may use self-signed certs
				},
			}
			// Set client timeout to slightly less than check timeout to get better error messages
			clientTimeout := time.Duration(c.Timeout) * time.Second
			client := &http.Client{
				Transport: tr,
				Timeout:   clientTimeout,
			}

			requestURL := fmt.Sprintf("%s://%s:%d%s", c.Scheme, c.Target, c.Port, u.Path)
			parsedURL, err := url.Parse(requestURL)
			if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
				res.Error = "invalid request URL"
				res.Debug = "URL failed validation: " + requestURL
				res.Status = false
				return res
			}
			req, err := http.NewRequest("GET", parsedURL.String(), nil)
			if err != nil {
				res.Error = "error creating web request"
				res.Debug = err.Error()
				res.Status = false
				return res
			}

			req.Header.Set("User-Agent", ua)

			// Store request info for timeout debugging
			res.Debug = fmt.Sprintf("Attempting GET %s", requestURL)

			resp, err := client.Do(req) // #nosec G704 -- URL is validated above; target comes from admin-controlled event.conf
			if err != nil {
				res.Error = "web request errored out"
				if strings.Contains(err.Error(), "Client.Timeout exceeded") {
					res.Debug = fmt.Sprintf("HTTP request to %s timed out after %v (TCP connection may have succeeded but server did not respond)", requestURL, clientTimeout)
				} else {
					res.Debug = err.Error() + " for url " + u.Path
				}
				res.Status = false
				return res
			}

			defer func() {
				if err := resp.Body.Close(); err != nil {
					slog.Error("failed to close http response body", "error", err)
				}
			}()

			if u.Status != 0 && resp.StatusCode != u.Status {
				res.Error = "status returned by webserver was incorrect"
				res.Debug = "status was " + strconv.Itoa(resp.StatusCode) + " wanted " + strconv.Itoa(u.Status) + " for url " + u.Path
				res.Status = false
				return res
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				res.Error = "error reading page content"
				res.Debug = "error was '" + err.Error() + "' for url " + u.Path
				res.Status = false
				return res
			}

			if u.Regex != "" {
				re, err := regexp.Compile(u.Regex)
				if err != nil {
					res.Error = "error compiling regex to match for web page"
					res.Debug = err.Error()
					res.Status = false
					return res
				}
				reFind := re.Find(body)
				if reFind == nil {
					res.Error = "didn't find regex on page"
					res.Debug = "couldn't find regex \"" + u.Regex + "\" for " + u.Path
					res.Status = false
					return res
				} else {
					res.Status = true
					res.Debug = "matched regex \"" + u.Regex + "\" for " + u.Path
					return res
				}
			}

			res.Status = true
			return res
		})
		response <- checkResult
		return
	}

	c.Service.Run(teamID, teamIdentifier, roundID, resultsChan, definition)
}

func (c *Web) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if c.ServiceType == "" {
		c.ServiceType = "Web"
	}
	if err := c.Service.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
		return err
	}

	if c.Scheme == "" {
		c.Scheme = "http"
	}
	if c.Display == "" {
		c.Display = "web"
	}
	if c.Name == "" {
		c.Name = box + "-" + c.Display
	}
	if c.Port == 0 {
		if c.Scheme == "https" {
			c.Port = 443
		} else {
			c.Port = 80
		}
	}
	if len(c.Url) == 0 {
		return errors.New("no urls defined")
	}
	if c.Scheme == "" {
		c.Scheme = "http"
	}
	for _, u := range c.Url {
		if u.Diff != 0 && u.CompareFile == "" {
			return errors.New("need compare file for diff in web")
		}
		if u.Path == "" {
			u.Path = "/"
		}
	}

	return nil
}
