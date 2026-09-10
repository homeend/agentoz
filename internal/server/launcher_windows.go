//go:build windows

package server

import "syscall"

// detachedProcess is DETACHED_PROCESS: no console inherited from erbrus.
const detachedProcess = 0x00000008

// detachAttr starts the GUI tool in its own process group without a
// console, so it survives erbrus and never shares its window.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess}
}
