//go:build windows

package process

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

type processTree struct{ job windows.Handle }

func prepareTree(command *exec.Cmd) error {
	attributes := windows.SysProcAttr{}
	if command.SysProcAttr != nil {
		attributes = *command.SysProcAttr
	}
	attributes.CreationFlags |= windows.CREATE_SUSPENDED
	command.SysProcAttr = &attributes
	return nil
}

func superviseTree(process *os.Process) (processTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return processTree{}, fmt.Errorf("create process job: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return processTree{}, fmt.Errorf("configure process job: %w", err)
	}
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return processTree{}, fmt.Errorf("open child process: %w", err)
	}
	defer windows.CloseHandle(handle)
	if err := windows.AssignProcessToJobObject(job, handle); err != nil {
		windows.CloseHandle(job)
		return processTree{}, fmt.Errorf("assign child process job: %w", err)
	}
	if err := resumeProcess(process.Pid); err != nil {
		windows.CloseHandle(job)
		return processTree{}, err
	}
	return processTree{job: job}, nil
}

func resumeProcess(pid int) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("inspect suspended child threads: %w", err)
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return fmt.Errorf("inspect first suspended child thread: %w", err)
	}
	for {
		if entry.OwnerProcessID == uint32(pid) {
			thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if openErr != nil {
				return fmt.Errorf("open suspended child thread: %w", openErr)
			}
			_, resumeErr := windows.ResumeThread(thread)
			closeErr := windows.CloseHandle(thread)
			if resumeErr != nil || closeErr != nil {
				return fmt.Errorf("resume suspended child thread: %w", errors.Join(resumeErr, closeErr))
			}
			return nil
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return errors.New("suspended child process has no thread")
			}
			return fmt.Errorf("inspect suspended child threads: %w", err)
		}
	}
}

func (tree processTree) kill() error {
	if err := windows.TerminateJobObject(tree.job, 1); err != nil {
		return fmt.Errorf("terminate process job: %w", err)
	}
	return nil
}

func (tree processTree) close() error { return windows.CloseHandle(tree.job) }
