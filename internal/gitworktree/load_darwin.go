package gitworktree

import (
	"encoding/binary"
	"syscall"
)

// loadAverage reads the one-minute load average from vm.loadavg, which is the
// figure uptime(1) prints. The kernel hands it over as a struct loadavg: three
// fixed-point uint32 values and the scale they are in, a long, at offset 16.
// syscall.Sysctl returns those bytes as a string with one trailing NUL
// trimmed, so the buffer is padded back to the struct's size before it is read.
func loadAverage() (float64, bool) {
	raw, err := syscall.Sysctl("vm.loadavg")
	if err != nil || len(raw) < 16 {
		return 0, false
	}
	buffer := make([]byte, 24)
	copy(buffer, raw)
	scale := binary.LittleEndian.Uint64(buffer[16:24])
	if scale == 0 {
		return 0, false
	}
	return float64(binary.LittleEndian.Uint32(buffer[0:4])) / float64(scale), true
}
