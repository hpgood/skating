//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// processAlive 判断指定 PID 的进程是否仍存在（Windows 下 os.FindProcess 在进程
// 不存在时返回错误）。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := os.FindProcess(pid)
	return err == nil
}

// stopProcess 用 taskkill /T /F 终止指定 PID 及其子进程树。
// 进程不存在时返回 errProcessGone。
func stopProcess(pid int) error {
	if pid <= 0 {
		return errors.New("invalid pid")
	}
	out, err := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").CombinedOutput()
	if err == nil {
		return nil
	}
	if !processAlive(pid) {
		return errProcessGone
	}
	return fmt.Errorf("taskkill: %v: %s", err, strings.TrimSpace(string(out)))
}
