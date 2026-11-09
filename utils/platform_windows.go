//go:build windows

package utils

import (
	"os"
	"runtime"
)

// Uname returns system information with the same keys as Python's platform.uname().
func Uname() map[string]any {
	host, _ := os.Hostname()
	return map[string]any{"system": "Windows", "node": host, "release": "", "version": "", "machine": runtime.GOARCH}
}
