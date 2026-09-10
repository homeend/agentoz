//go:build !windows

package server

import "syscall"

// detachAttr puts the GUI tool in its own session so it survives erbrus
// and never holds the server's controlling terminal.
func detachAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
