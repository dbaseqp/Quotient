package checks

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
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
		u := c.Url[rand.IntN(len(c.Url))] // #nosec G404 -- non-crypto selection of URL to test

		tr := newBrowserTransport(randomClientOS())
		// Browsers keep idle connections for minutes (Firefox 115 s), so the
		// server's keep-alive timeout usually ends them; closing right after
		// the response is what scripts do.
		defer time.AfterFunc(30*time.Second+rand.N(80*time.Second), tr.CloseIdleConnections) // #nosec G404 -- non-crypto timing jitter
		clientTimeout := time.Duration(c.Timeout) * time.Second
		client := &http.Client{Transport: tr, Timeout: clientTimeout}

		requestURL := fmt.Sprintf("%s://%s:%d%s", c.Scheme, c.Target, c.Port, u.Path)
		parsedURL, err := url.Parse(requestURL)
		if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
			checkResult.Error = "invalid request URL"
			checkResult.Debug = "URL failed validation: " + requestURL
			response <- checkResult
			return
		}
		req, err := http.NewRequest("GET", parsedURL.String(), nil)
		if err != nil {
			checkResult.Error = "error creating web request"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		// Store request info for timeout debugging
		checkResult.Debug = fmt.Sprintf("Attempting GET %s", requestURL)

		resp, err := client.Do(req) // #nosec G704 -- URL is validated above; target comes from admin-controlled event.conf
		if err != nil {
			checkResult.Error = "web request errored out"
			if strings.Contains(err.Error(), "Client.Timeout exceeded") {
				checkResult.Debug = fmt.Sprintf("HTTP request to %s timed out after %v (TCP connection may have succeeded but server did not respond)", requestURL, clientTimeout)
			} else {
				checkResult.Debug = err.Error() + " for url " + u.Path
			}
			response <- checkResult
			return
		}

		defer func() {
			if err := resp.Body.Close(); err != nil {
				slog.Error("failed to close http response body", "error", err)
			}
		}()

		if u.Status != 0 && resp.StatusCode != u.Status {
			checkResult.Error = "status returned by webserver was incorrect"
			checkResult.Debug = "status was " + strconv.Itoa(resp.StatusCode) + " wanted " + strconv.Itoa(u.Status) + " for url " + u.Path
			response <- checkResult
			return
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxWebBodyBytes))
		if err != nil {
			checkResult.Error = "error reading page content"
			checkResult.Debug = "error was '" + err.Error() + "' for url " + u.Path
			response <- checkResult
			return
		}

		if u.Regex != "" {
			re, err := regexp.Compile(u.Regex)
			if err != nil {
				checkResult.Error = "error compiling regex to match for web page"
				checkResult.Debug = err.Error()
				response <- checkResult
				return
			}
			reFind := re.Find(body)
			if reFind == nil {
				checkResult.Error = "didn't find regex on page"
				checkResult.Debug = "couldn't find regex \"" + u.Regex + "\" for " + u.Path
				response <- checkResult
				return
			} else {
				checkResult.Status = true
				checkResult.Debug = "matched regex \"" + u.Regex + "\" for " + u.Path
				response <- checkResult
				return
			}
		}

		checkResult.Status = true
		response <- checkResult
	}

	c.Service.Run(teamID, teamIdentifier, roundID, resultsChan, definition)
}

// maxWebBodyBytes caps how much of a page, after its Content-Encoding is
// undone, the web check reads; a Regex must match within this prefix.
const maxWebBodyBytes = 10 << 20

func (c *Web) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if c.ServiceType == "" {
		c.ServiceType = "Web"
	}
	if err := c.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
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
