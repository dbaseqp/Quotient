package checks

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/go-ntlmssp"
	"github.com/masterzen/winrm"
	"github.com/masterzen/winrm/soap"
)

// winrmUserAgent is the User-Agent of the Windows WinRM client (WinRS and PowerShell remoting).
const winrmUserAgent = "Microsoft WinRM Client"

// winrmTransport is masterzen/winrm's NTLM transport (ClientNTLM) with the User-Agent of the
// Windows WinRM client and without Go's "Accept-Encoding: gzip". Authentication (go-ntlmssp
// Negotiator), SOAP bodies and response handling are masterzen's.
type winrmTransport struct {
	username string
	password string
	url      string
	client   *http.Client
}

func (t *winrmTransport) Transport(endpoint *winrm.Endpoint) error {
	scheme := "http"
	if endpoint.HTTPS {
		scheme = "https"
	}
	t.url = scheme + "://" + net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port)) + "/wsman"
	// The sockets announce the window scale of the Windows host the User-Agent claims.
	dialer := clientWindows.dialer(30 * time.Second)
	dialer.KeepAlive = 30 * time.Second
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: endpoint.Insecure, // #nosec G402 -- competition services may use self-signed certs
			ServerName:         endpoint.TLSServerName,
		},
		DialContext:           dialer.DialContext,
		ResponseHeaderTimeout: endpoint.Timeout,
		DisableCompression:    true,
	}
	t.client = &http.Client{Transport: ntlmssp.Negotiator{RoundTripper: transport}}
	return nil
}

// Post mirrors masterzen's clientRequest.Post, including its error strings (command.go matches
// "OperationTimeout" in them). Any SOAP reply is returned as the body whatever its HTTP status:
// masterzen's status check assigns to its unnamed results in a deferred function, which has no
// effect.
func (t *winrmTransport) Post(_ *winrm.Client, request *soap.SoapMessage) (string, error) {
	req, err := http.NewRequest(http.MethodPost, t.url, strings.NewReader(request.String()))
	if err != nil {
		return "", fmt.Errorf("impossible to create http request %w", err)
	}
	req.Header.Set("Content-Type", "application/soap+xml;charset=UTF-8")
	req.Header.Set("User-Agent", winrmUserAgent)
	req.SetBasicAuth(t.username, t.password) // the Negotiator turns these into NTLM
	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("unknown error %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if !strings.Contains(resp.Header.Get("Content-Type"), "application/soap+xml") {
		return "", fmt.Errorf("http response error: %d - invalid content type", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("http response error: %d - error while reading request body %w", resp.StatusCode, err)
	}
	return string(body), nil
}
