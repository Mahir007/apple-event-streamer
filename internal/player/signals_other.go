//go:build !unix

package player

import (
	"os"
	"syscall"
)

// On Windows, closing the console, logging off and shutting down arrive as
// SIGTERM; the process has a few seconds before Windows ends it.
var interruptSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}
