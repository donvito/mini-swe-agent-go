//go:build unix

package utils

import (
	"os"

	"golang.org/x/sys/unix"
)

// Uname returns system information with the same keys as Python's platform.uname().
func Uname() map[string]any {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		host, _ := os.Hostname()
		return map[string]any{"system": "", "node": host, "release": "", "version": "", "machine": ""}
	}
	return map[string]any{
		"system":  unix.ByteSliceToString(u.Sysname[:]),
		"node":    unix.ByteSliceToString(u.Nodename[:]),
		"release": unix.ByteSliceToString(u.Release[:]),
		"version": unix.ByteSliceToString(u.Version[:]),
		"machine": unix.ByteSliceToString(u.Machine[:]),
	}
}
