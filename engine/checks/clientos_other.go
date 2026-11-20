//go:build !linux

package checks

import "syscall"

// control is a no-op off Linux: the runner image is Linux, and these socket
// options are set the Linux way.
func (o clientOS) control(string, string, syscall.RawConn) error { return nil }
