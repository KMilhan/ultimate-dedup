package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunWrapper(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "A")
	reference := filepath.Join(root, "B")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(reference, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reference, "b.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"-source", source, "-reference", reference, "-workers", "1", "-batch-size", "10"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected run wrapper code 0, got %d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "matched in source: 1") {
		t.Fatalf("unexpected output: %q", stdout.String())
	}
}

func TestMainUsesExitFunc(t *testing.T) {
	oldArgs := os.Args
	oldExit := exitFunc
	defer func() {
		os.Args = oldArgs
		exitFunc = oldExit
	}()

	os.Args = []string{"ultimate-dedup", "-h"}

	var gotCode int
	called := false
	exitFunc = func(code int) {
		called = true
		gotCode = code
	}

	main()

	if !called {
		t.Fatalf("expected exit function to be called")
	}
	if gotCode != 0 {
		t.Fatalf("expected exit code 0, got %d", gotCode)
	}
}
