package checks

import (
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// clientOS is the operating system a check claims to run on, through its
// User-Agent, workstation name or client banner. Its sockets announce that
// OS's TCP window scale in the SYN where the host allows it, and macOS
// sockets its 65535-byte SYN window.
type clientOS int

const (
	clientLinux clientOS = iota
	clientWindows
	clientMacOS
)

func (o clientOS) String() string {
	switch o {
	case clientWindows:
		return "windows"
	case clientMacOS:
		return "macos"
	}
	return "linux"
}

// windowClamp caps the TCP receive window so that Linux announces the claimed
// OS's window scale in the SYN: 8 for Windows, 7 for Linux, 6 for macOS.
// Linux derives the scale from the larger of net.ipv4.tcp_rmem[2] and
// net.core.rmem_max, capped by the clamp, so a clamp can only lower it: where
// both sysctls are below 8 MiB (scale 7, the Linux default of 6 MiB) a
// Windows socket still announces 7.
func (o clientOS) windowClamp() int {
	switch o {
	case clientWindows:
		return 8 << 20
	case clientMacOS:
		return 2 << 20
	}
	return 4 << 20
}

// macOSRcvBuf is the SO_RCVBUF a macOS socket asks for. Linux writes its SYN
// window as the socket's receive space rounded down to a multiple of the MSS
// (64240 with MSS 1460); macOS sends 65535. A receive buffer of at least
// 2 MiB makes the space larger than 65535, so the SYN carries 65535, and
// keeps the window scale at 6 (with a locked buffer Linux takes the scale
// from the buffer, capped by the 2 MiB clamp).
const macOSRcvBuf = 2 << 20

// rcvBufFits reports whether net.core.rmem_max (host-wide; not settable per
// network namespace) lets SO_RCVBUF reach macOSRcvBuf. Below it a locked
// smaller buffer would announce a scale under 6, so the socket keeps its
// autotuned buffer and Linux's SYN window.
var rcvBufFits = func() bool {
	b, err := os.ReadFile("/proc/sys/net/core/rmem_max")
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return err == nil && n >= macOSRcvBuf
}()

// rcvBuf is the SO_RCVBUF o's sockets set before connect; 0: leave the
// kernel's autotuned default (Windows and Linux send Linux's 64240 window,
// which is also Windows 10/11's).
func (o clientOS) rcvBuf() int {
	if o == clientMacOS && rcvBufFits {
		return macOSRcvBuf
	}
	return 0
}

// dialer returns a net.Dialer whose sockets carry o's window settings.
func (o clientOS) dialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{Timeout: timeout, Control: o.control}
}
