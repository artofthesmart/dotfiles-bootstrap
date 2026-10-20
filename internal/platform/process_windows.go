//go:build windows

package platform

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	openProcess        = kernel32.NewProc("OpenProcess")
	getExitCodeProcess = kernel32.NewProc("GetExitCodeProcess")
)

func processRunning(process *os.Process) (bool, error) {
	handle, _, callErr := openProcess.Call(processQueryLimitedInformation, 0, uintptr(process.Pid))
	if handle == 0 {
		return false, callErr
	}
	defer syscall.CloseHandle(syscall.Handle(handle))
	var exitCode uint32
	ok, _, callErr := getExitCodeProcess.Call(handle, uintptr(unsafe.Pointer(&exitCode)))
	if ok == 0 {
		return false, fmt.Errorf("query process %d: %w", process.Pid, callErr)
	}
	return exitCode == stillActive, nil
}
