package checkpoint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureAndRestore(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	target := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(target, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := New(home, ws, "s1")
	snap, err := m.Capture(1, []string{"a.txt", "new.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Files) != 2 {
		t.Fatalf("files = %d", len(snap.Files))
	}

	if err := os.WriteFile(target, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "new.txt"), []byte("created"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := m.Restore(snap); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatalf("a.txt = %q", got)
	}
	if _, err := os.Stat(filepath.Join(ws, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("new.txt should have been removed, err=%v", err)
	}
}

func TestLastRestoresNewest(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(home, ws, "s1")
	if _, err := m.Capture(1, []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Capture(2, []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("v3"), 0o644); err != nil {
		t.Fatal(err)
	}

	snap, err := m.RestoreLast()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Turn != 2 {
		t.Fatalf("restored turn %d", snap.Turn)
	}
	got, _ := os.ReadFile(filepath.Join(ws, "a.txt"))
	if string(got) != "v2" {
		t.Fatalf("a.txt = %q", got)
	}
}
