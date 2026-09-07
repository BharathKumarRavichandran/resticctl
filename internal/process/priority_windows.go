//go:build windows

package process

import (
	"fmt"
	"os/exec"
	"syscall"
)

const (
	idlePriorityClass        = 0x00000040
	belowNormalPriorityClass = 0x00004000
	normalPriorityClass      = 0x00000020
	aboveNormalPriorityClass = 0x00008000
	highPriorityClass        = 0x00000080
)

func applyPriority(command *exec.Cmd, configured any) error {
	priority, ok := configured.(Priority)
	if !ok {
		return nil
	}
	name := priority.Windows
	if name == "" {
		name = priority.Portable
	}
	if name == "" {
		return nil
	}
	classes := map[string]uint32{
		"idle": idlePriorityClass, "background": belowNormalPriorityClass,
		"below-normal": belowNormalPriorityClass, "normal": normalPriorityClass,
		"above-normal": aboveNormalPriorityClass, "high": highPriorityClass,
	}
	class, ok := classes[name]
	if !ok {
		return fmt.Errorf("unsupported Windows priority class %q", name)
	}
	attributes := command.SysProcAttr
	if attributes == nil {
		attributes = &syscall.SysProcAttr{}
		command.SysProcAttr = attributes
	}
	attributes.CreationFlags |= class
	return nil
}
