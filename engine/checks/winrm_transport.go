package checks

import (
	"crypto/tls"
	"errors"
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

// winrmTransport is masterzen/winrm's NTLM transport (ClientNTLM) with the HTTP identity of the
// Windows client: masterzen leaves Go's "Go-http-client/1.1" User-Agent and "Accept-Encoding: gzip"
// on every request, which no Windows or pywinrm client sends. Authentication (go-ntlmssp Negotiator),
// SOAP bodies and response handling are unchanged.
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
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: endpoint.Insecure, // #nosec G402 -- competition services may use self-signed certs
			ServerName:         endpoint.TLSServerName,
		},
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ResponseHeaderTimeout: endpoint.Timeout,
		DisableCompression:    true,
	}
	t.client = &http.Client{Transport: ntlmssp.Negotiator{RoundTripper: transport}}
	return nil
}

// Post mirrors masterzen's clientRequest.Post, including its error strings (command.go matches
// "OperationTimeout" in them).
func (t *winrmTransport) Post(_ *winrm.Client, request *soap.SoapMessage) (string, error) {
	//nolint:noctx // the endpoint's ResponseHeaderTimeout and the check timeout bound the request
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
		return "", fmt.Errorf("http response error: %d - %w", resp.StatusCode, errors.New("invalid content type"))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("http response error: %d - %w", resp.StatusCode, fmt.Errorf("error while reading request body %w", err))
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http error %d: %s", resp.StatusCode, body)
	}
	return string(body), nil
}
