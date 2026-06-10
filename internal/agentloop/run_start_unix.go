//go:build darwin || linux

package agentloop

import "syscall"

func runLaunchSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
