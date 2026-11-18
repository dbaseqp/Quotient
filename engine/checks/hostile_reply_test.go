package checks

// A team controls the service a check talks to, so every byte a check parses
// is hostile. A panic while parsing a reply must fail that check rather than
// kill the runner process and every task it holds. Each test serves a hostile
// reply from a fake server in the parent test process and runs the real check
// in a child process, asserting the child survives and reports the service down.

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	ber "github.com/go-asn1-ber/asn1-ber"
	"github.com/stretchr/testify/require"
)

const hostileChildEnv = "HOSTILE_REPLY_CHILD"

// hostileServe accepts connections on a loopback port and hands each to handle.
func hostileServe(t *testing.T, handle func(net.Conn)) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				handle(c)
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// hostileChild reports whether this process is the child for the current test.
// In the child it runs the check against the port the parent passed and prints
// the result; in the parent it runs the child and asserts it did not crash.
func hostileChild(t *testing.T, port int, check func(port int) Runner) {
	t.Helper()
	if os.Getenv(hostileChildEnv) == t.Name() {
		p, _ := strconv.Atoi(os.Getenv("HOSTILE_REPLY_PORT"))
		results := make(chan Result, 1)
		check(p).Run(1, "01", 1, results)
		select {
		case r := <-results:
			fmt.Printf("CHECK status=%v error=%q debug=%q\n", r.Status, r.Error, r.Debug)
		case <-time.After(10 * time.Second):
			fmt.Println("CHECK no result")
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run", "^"+t.Name()+"$", "-test.v")
	cmd.Env = append(os.Environ(), hostileChildEnv+"="+t.Name(), "HOSTILE_REPLY_PORT="+strconv.Itoa(port))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "runner process crashed on a team's hostile reply:\n%s", out)
	require.Contains(t, string(out), "CHECK status=false", string(out))
}

func hostileCreds() []TaskCredential {
	return []TaskCredential{{Username: "u", Password: "p", Credlist: "l"}}
}

// FTP: jlaffaye/ftp v0.2.0 epsv() slices line[start+3:end] where end is the
// last '|'. A team FTP server answering EPSV with "229 (|||)" makes end <
// start+3 and the slice panics in the check goroutine.
func TestFtpHostileEpsvReplyFailsCheck(t *testing.T) {
	port := 0
	if os.Getenv(hostileChildEnv) == "" {
		port = hostileServe(t, func(c net.Conn) {
			r := bufio.NewReader(c)
			fmt.Fprint(c, "220 hi\r\n")
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				switch cmd := strings.ToUpper(strings.Fields(line + " x")[0]); cmd {
				case "USER":
					fmt.Fprint(c, "230 logged in\r\n")
				case "FEAT":
					fmt.Fprint(c, "211 End\r\n")
				case "EPSV":
					fmt.Fprint(c, "229 Entering Extended Passive Mode (|||)\r\n")
				case "QUIT":
					fmt.Fprint(c, "221 bye\r\n")
					return
				default:
					fmt.Fprint(c, "200 ok\r\n")
				}
			}
		})
	}
	hostileChild(t, port, func(p int) Runner {
		c := &Ftp{File: []FtpFile{{Name: "flag.txt"}}}
		require.NoError(t, c.Verify("box01", "127.0.0.1", 10, 5, 0, 0))
		c.Port = p
		return c
	})
}

// POP3: knadh/go-pop3 v1.0.0 Stat() indexes bytes.Fields(reply)[0] without a
// length check. A team POP3 server answering STAT with a bare "+OK" panics the
// check goroutine after a successful login.
func TestPop3HostileStatReplyFailsCheck(t *testing.T) {
	port := 0
	if os.Getenv(hostileChildEnv) == "" {
		port = hostileServe(t, func(c net.Conn) {
			r := bufio.NewReader(c)
			fmt.Fprint(c, "+OK ready\r\n")
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				switch strings.ToUpper(strings.Fields(line + " x")[0]) {
				case "STAT":
					fmt.Fprint(c, "+OK\r\n")
				case "QUIT":
					fmt.Fprint(c, "+OK bye\r\n")
					return
				default:
					fmt.Fprint(c, "+OK fine\r\n")
				}
			}
		})
	}
	hostileChild(t, port, func(p int) Runner {
		c := &Pop3{Service: Service{CredLists: []string{"l"}}}
		require.NoError(t, c.Verify("box01", "127.0.0.1", 10, 5, 0, 0))
		c.Port = p
		c.TaskCredentials = hostileCreds()
		return c
	})
}

// LDAP: go-ldap v3.4.8 GetLDAPError asserts resultCode.Value.(int64) after
// checking only the tag number. A BindResponse whose resultCode is encoded as
// a constructed ENUMERATED (0x2a 0x00) has a nil Value, and SimpleBind (called
// from the check goroutine, outside the reader's recover) panics.
func TestLdapHostileBindResponseFailsCheck(t *testing.T) {
	port := 0
	if os.Getenv(hostileChildEnv) == "" {
		port = hostileServe(t, func(c net.Conn) {
			req, err := ber.ReadPacket(c)
			if err != nil || len(req.Children) < 1 {
				return
			}
			msgID, _ := req.Children[0].Value.(int64)
			resp := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "")
			resp.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, msgID, ""))
			bind := ber.Encode(ber.ClassApplication, ber.TypeConstructed, 1, nil, "") // BindResponse
			bind.AppendChild(ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagEnumerated, nil, ""))
			bind.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", ""))
			bind.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", ""))
			resp.AppendChild(bind)
			_, _ = c.Write(resp.Bytes())
			_, _ = ber.ReadPacket(c) // wait for unbind/close
		})
	}
	hostileChild(t, port, func(p int) Runner {
		c := &Ldap{Service: Service{CredLists: []string{"l"}}, Domain: "team.local"}
		require.NoError(t, c.Verify("box01", "127.0.0.1", 10, 5, 0, 0))
		c.Port = p
		c.TaskCredentials = hostileCreds()
		return c
	})
}

// SMB: hirochachacha/go-smb2 v1.1.0 conn.runReciever reads a direct-TCP frame
// of whatever length the server announces and calls PacketCodec(pkt).
// NextCommand(), which reads pkt[20:24] without a length check (and slices
// pkt[:NextCommand] unchecked). A team SMB server answering the NEGOTIATE with
// a 1-byte frame panics the receiver goroutine.
