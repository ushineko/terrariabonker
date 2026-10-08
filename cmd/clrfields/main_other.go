//go:build !windows

// Command clrfields is Windows-only; see main.go.
package main

import (
	"fmt"
	"os"
)

// The CLR's field table is only there to read when the game runs on .NET
// Framework, which is native Windows. Under Proton the runtime is mono: use
// cmd/monofields.
func main() {
	fmt.Fprintln(os.Stderr, "clrfields reads a game running on .NET Framework (native Windows); under Proton use monofields")
	os.Exit(2)
}
