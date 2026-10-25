package main

// Version is the CLI version. It is overridden at build time:
//
//	go build -ldflags "-X main.Version=v0.3.0" ./cmd/rakit
var Version = "dev"
