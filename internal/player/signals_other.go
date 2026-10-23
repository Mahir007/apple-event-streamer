//go:build !unix

package player

import "os"

var interruptSignals = []os.Signal{os.Interrupt}
