package main

import (
	"flag"
	"fmt"
	"os"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("rakitkode %s\n", version)
		os.Exit(0)
	}

	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: rakitkode <command> [args]")
		os.Exit(2)
	}

	switch flag.Arg(0) {
	case "help", "-h", "--help":
		fmt.Println(`rakitkode - build things with code

Usage:
  rakitkode <command> [args]
  rakitkode -version`)
	default:
		fmt.Fprintf(os.Stderr, "rakitkode: unknown command %q\n", flag.Arg(0))
		os.Exit(2)
	}
}
