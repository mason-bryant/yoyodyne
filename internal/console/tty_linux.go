//go:build linux

package console

import "syscall"

const (
	readTermios  = syscall.TCGETS
	writeTermios = syscall.TCSETS
)
