// Command gx runs a command the way the app's Go code would, with a raw execve
// that termux-exec's LD_PRELOAD can't intercept.
package main

import (
	"fmt"
	"os"
	"os/exec"
)

func main() {
	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Println("gx:", err)
		os.Exit(1)
	}
	fmt.Println("gx: ok")
}
