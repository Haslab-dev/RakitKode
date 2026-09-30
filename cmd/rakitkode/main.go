// RakitKode is an AI coding agent in your terminal.
package main

import "github.com/Haslab-dev/RakitKode/internal/cmd"

// version is injected at build time via -ldflags.
var version = "dev"

func main() {
	cmd.SetVersion(version)
	cmd.Execute()
}
