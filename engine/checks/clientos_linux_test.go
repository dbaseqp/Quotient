//go:build linux

package checks

import (
	"encoding/binary"
	"math/bits"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sockoptInt(t *testing.T, conn net.Conn, level, opt int) int {
	t.Helper()
	sc, ok := conn.(syscall.Conn)
	require.True(t, ok)
	raw, err := sc.SyscallConn()
	require.NoError(t, err)
	var v int
	var gerr error
	require.NoError(t, raw.Control(func(fd uintptr) { v, gerr = syscall.GetsockoptInt(int(fd), level, opt) }))
	require.NoError(t, gerr)
	return v
}

// TestClientOSSYNOnTheWire captures each profile's SYN on lo (needs
// CAP_NET_RAW, so it skips unprivileged) and checks the window scale and,
// for macOS, the window a team would see.
func TestClientOSSYNOnTheWire(t *testing.T) {
	lo, err := net.InterfaceByName("lo")
	require.NoError(t, err)
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, int(htons(syscall.ETH_P_ALL)))
	if err != nil {
		t.Skipf("no packet socket (%v): run as root to check the SYN on the wire", err)
	}
	defer syscall.Close(fd)
	require.NoError(t, syscall.Bind(fd, &syscall.SockaddrLinklayer{Protocol: htons(syscall.ETH_P_ALL), Ifindex: lo.Index}))
	tv := syscall.NsecToTimeval(int64(200 * time.Millisecond))
	require.NoError(t, syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv))

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	host := hostWindowScale(t)

	for _, tc := range []struct {
		os    clientOS
		scale int
	}{{clientWindows, 8}, {clientLinux, 7}, {clientMacOS, 6}} {
		conn, err := tc.os.dialer(time.Second).Dial("tcp4", ln.Addr().String())
		require.NoError(t, err)
		sport := uint16(conn.LocalAddr().(*net.TCPAddr).Port)
		scale, win := captureSYN(t, fd, sport, port)
		_ = conn.Close()
		assert.Equal(t, min(tc.scale, host), scale, "%s: window scale in the SYN (host allows up to %d)", tc.os, host)
		if tc.os == clientMacOS && rcvBufFits {
			assert.Equal(t, 65535, win, "macOS SYN window")
		}
	}
}

// hostWindowScale is the window scale this host's sysctls allow a socket.
func hostWindowScale(t *testing.T) int {
	read := func(p string) int {
		b, err := os.ReadFile(p)
		require.NoError(t, err)
		f := strings.Fields(string(b))
		n, err := strconv.Atoi(f[len(f)-1])
		require.NoError(t, err)
		return n
	}
	space := max(read("/proc/sys/net/ipv4/tcp_rmem"), read("/proc/sys/net/core/rmem_max"))
	return min(max(bits.Len(uint(space))-1-15, 0), 14)
}

func captureSYN(t *testing.T, fd int, sport, dport uint16) (scale, win int) {
	t.Helper()
	buf := make([]byte, 65536)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil || n < 14+20+20 {
			continue
		}
		ip := buf[14:n]
		if ip[0]>>4 != 4 || ip[9] != syscall.IPPROTO_TCP {
			continue
		}
		tcp := ip[int(ip[0]&15)*4:]
		if binary.BigEndian.Uint16(tcp[0:]) != sport || binary.BigEndian.Uint16(tcp[2:]) != dport || tcp[13]&0x12 != 0x02 {
			continue
		}
		opts := tcp[20 : int(tcp[12]>>4)*4]
		scale = -1
		for i := 0; i < len(opts); {
			switch opts[i] {
			case 0:
				i = len(opts)
			case 1:
				i++
			default:
				if opts[i] == 3 {
					scale = int(opts[i+2])
				}
				i += int(opts[i+1])
			}
		}
		return scale, int(binary.BigEndian.Uint16(tcp[14:]))
	}
	t.Fatalf("no SYN from port %d captured", sport)
	return 0, 0
}

func htons(v uint16) uint16 { return v<<8 | v>>8 }

// macOS sockets lock a 2 MiB receive buffer (the kernel reports it doubled)
// when rmem_max allows; the others keep the kernel's autotuned buffer.
func TestClientOSMacOSReceiveBuffer(t *testing.T) {
	if !rcvBufFits {
		t.Skip("net.core.rmem_max below 2 MiB: macOS sockets keep the default buffer")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	def, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	defer def.Close()
	for o, want := range map[clientOS]int{clientMacOS: 2 * macOSRcvBuf, clientWindows: sockoptInt(t, def, syscall.SOL_SOCKET, syscall.SO_RCVBUF), clientLinux: sockoptInt(t, def, syscall.SOL_SOCKET, syscall.SO_RCVBUF)} {
		conn, err := o.dialer(time.Second).Dial("tcp", ln.Addr().String())
		require.NoError(t, err)
		assert.Equal(t, want, sockoptInt(t, conn, syscall.SOL_SOCKET, syscall.SO_RCVBUF), o.String())
		_ = conn.Close()
	}
}
