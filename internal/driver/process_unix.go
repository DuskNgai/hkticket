//go:build darwin || linux

package driver

import (
	"errors"
	"syscall"
)

// processAlive 判断进程是否仍在运行(EPERM 说明进程存在,只是无权发信号)
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
