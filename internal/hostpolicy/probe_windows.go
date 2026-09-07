//go:build windows

package hostpolicy

import (
	"context"
	"errors"
	"net"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

type systemProbe struct{}

var kernel32 = windows.NewLazySystemDLL("kernel32.dll")
var globalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
var getSystemPowerStatus = kernel32.NewProc("GetSystemPowerStatus")
var setThreadExecutionState = kernel32.NewProc("SetThreadExecutionState")

type memoryStatus struct {
	Length, MemoryLoad                                 uint32
	TotalPhys, AvailPhys, TotalPageFile, AvailPageFile uint64
	TotalVirtual, AvailVirtual, AvailExtendedVirtual   uint64
}

type powerStatus struct {
	ACLineStatus, BatteryFlag, BatteryLifePercent, SystemStatusFlag byte
	BatteryLifeTime, BatteryFullLifeTime                            uint32
}

func (systemProbe) AvailableMemory() (uint64, error) {
	status := memoryStatus{Length: uint32(unsafe.Sizeof(memoryStatus{}))}
	result, _, callErr := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if result == 0 {
		return 0, callErr
	}
	return status.AvailPhys, nil
}

func (systemProbe) OnACPower() (bool, error) {
	var status powerStatus
	result, _, callErr := getSystemPowerStatus.Call(uintptr(unsafe.Pointer(&status)))
	if result == 0 {
		return false, callErr
	}
	if status.ACLineStatus == 255 {
		return false, errors.New("AC power state is unknown")
	}
	return status.ACLineStatus == 1, nil
}

func (systemProbe) NetworkOnline() (bool, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return false, err
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0 {
			return true, nil
		}
	}
	return false, nil
}

func (systemProbe) ProcessActive(pid int) (bool, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	windows.CloseHandle(handle)
	return true, nil
}

func (systemProbe) PreventSleep(context.Context) (func() error, error) {
	const continuous = 0x80000000
	const systemRequired = 0x00000001
	runtime.LockOSThread()
	previous, _, callErr := setThreadExecutionState.Call(continuous | systemRequired)
	if previous == 0 {
		runtime.UnlockOSThread()
		return nil, callErr
	}
	return func() error {
		defer runtime.UnlockOSThread()
		result, _, err := setThreadExecutionState.Call(continuous)
		if result == 0 {
			return err
		}
		return nil
	}, nil
}
