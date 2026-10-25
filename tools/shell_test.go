package tools

import (
	"os"
	"testing"
)

func TestShellCommandResolves(t *testing.T) {
	sh, args := shellCommand("echo hi")
	if sh == "" {
		t.Fatal("empty shell")
	}
	if _, err := os.Stat(sh); err != nil {
		t.Fatalf("shell %q does not exist: %v", sh, err)
	}
	if len(args) == 0 || args[len(args)-1] != "echo hi" {
		t.Fatalf("unexpected args: %#v", args)
	}
}
