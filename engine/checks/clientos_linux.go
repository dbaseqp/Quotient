//go:build linux

package checks

import (
	"strings"
	"syscall"
)

// control sets o's window clamp, and its receive buffer where it has one, on
// a TCP socket before it connects, so the SYN carries them.
func (o clientOS) control(network, _ string, c syscall.RawConn) error {
	if !strings.HasPrefix(network, "tcp") {
		return nil
	}
	var serr error
	err := c.Control(func(fd uintptr) {
		s := int(fd)
		serr = syscall.SetsockoptInt(s, syscall.IPPROTO_TCP, syscall.TCP_WINDOW_CLAMP, o.windowClamp())
		if serr == nil && o.rcvBuf() > 0 {
			serr = syscall.SetsockoptInt(s, syscall.SOL_SOCKET, syscall.SO_RCVBUF, o.rcvBuf())
		}
	})
	if err != nil {
		return err
	}
	return serr
}
