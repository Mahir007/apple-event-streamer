//go:build unix

package player

import (
	"os"
	"syscall"
)

var interruptSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}
