package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseHelp(t *testing.T) {
	var out bytes.Buffer
	parsed, err := Parse([]string{"-h"}, &out)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if !parsed.help {
		t.Fatalf("expected help=true")
	}
	if out.Len() == 0 {
		t.Fatalf("expected usage text output")
	}
	if !strings.Contains(out.String(), "--source") {
		t.Fatalf("expected GNU-style long flags in usage output, got %q", out.String())
	}
}

func TestParseUnexpectedPositionalArgs(t *testing.T) {
	var out bytes.Buffer
	_, err := Parse([]string{"-source", "/a", "-reference", "/b", "extra"}, &out)
	if err == nil {
		t.Fatalf("expected positional args error")
	}
}

func TestParseMapsFlags(t *testing.T) {
	var out bytes.Buffer
	parsed, err := Parse([]string{
		"-source", "/a",
		"-reference", "/b",
		"-workers", "7",
		"-batch-size", "99",
		"-hash", "sha256",
		"-apply",
		"-no-verify",
		"-verbose",
		"-progress",
	}, &out)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.cfg.SourceDir != "/a" || parsed.cfg.ReferenceDir != "/b" {
		t.Fatalf("unexpected source/reference mapping")
	}
	if parsed.cfg.Workers != 7 || parsed.cfg.BatchSize != 99 {
		t.Fatalf("unexpected workers/batch mapping")
	}
	if parsed.cfg.Hash != "sha256" || !parsed.cfg.Apply || parsed.cfg.Verify {
		t.Fatalf("unexpected hash/apply/verify mapping: %#v", parsed.cfg)
	}
	if !parsed.cfg.Verbose || !parsed.cfg.Progress {
		t.Fatalf("expected verbose and progress flags to map: %#v", parsed.cfg)
	}
}

func TestParseInPlaceMapsFlags(t *testing.T) {
	var out bytes.Buffer
	parsed, err := Parse([]string{
		"-source", "/a",
		"-in-place",
	}, &out)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.cfg.SourceDir != "/a" {
		t.Fatalf("unexpected source mapping")
	}
	if !parsed.cfg.InPlace {
		t.Fatalf("expected in-place mode to be enabled")
	}
	if parsed.cfg.ReferenceDir != "" {
		t.Fatalf("expected empty reference in in-place mode parse")
	}
}

func TestRunParseError(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"-workers", "oops"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "error:") {
		t.Fatalf("expected stderr error output, got %q", stderr.String())
	}
}

func TestRunNoArgsShowsGuidance(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	errOut := stderr.String()
	for _, token := range []string{
		"removes files by content match",
		"Modes:",
		"Examples:",
		"Usage of ultimate-dedup:",
		"--source",
		"--reference",
	} {
		if !strings.Contains(errOut, token) {
			t.Fatalf("expected stderr to contain %q, got %q", token, errOut)
		}
	}
}

func TestRunHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"-h"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if stderr.Len() == 0 {
		t.Fatalf("expected help usage on stderr")
	}
}

func TestRunDedupError(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"-source", "/tmp/only-source"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "error:") {
		t.Fatalf("expected dedup error on stderr, got %q", stderr.String())
	}
}

func TestRunSuccessAndSummary(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "A")
	reference := filepath.Join(root, "B")
	mustMkdirAll(t, source)
	mustMkdirAll(t, reference)

	mustWriteFile(t, filepath.Join(source, "a.txt"), []byte("hello"))
	mustWriteFile(t, filepath.Join(reference, "b.txt"), []byte("hello"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{
		"-source", source,
		"-reference", reference,
		"-workers", "1",
		"-batch-size", "10",
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	for _, token := range []string{
		"mode: dry-run",
		"workers:",
		"matched in source: 1",
		"reclaimable:",
	} {
		if !strings.Contains(out, token) {
			t.Fatalf("expected output to contain %q, got: %s", token, out)
		}
	}
}

func TestRunApplySummaryIncludesDeleteLine(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "A")
	reference := filepath.Join(root, "B")
	mustMkdirAll(t, source)
	mustMkdirAll(t, reference)

	mustWriteFile(t, filepath.Join(source, "a.txt"), []byte("hello"))
	mustWriteFile(t, filepath.Join(reference, "b.txt"), []byte("hello"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{
		"-source", source,
		"-reference", reference,
		"-workers", "1",
		"-batch-size", "10",
		"-apply",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "deleted: 1 files") {
		t.Fatalf("expected apply summary delete line, got %q", stdout.String())
	}
}

func TestRunVerboseAndProgressWritesLogs(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "A")
	reference := filepath.Join(root, "B")
	mustMkdirAll(t, source)
	mustMkdirAll(t, reference)

	mustWriteFile(t, filepath.Join(source, "a.txt"), []byte("hello"))
	mustWriteFile(t, filepath.Join(reference, "b.txt"), []byte("hello"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{
		"-source", source,
		"-reference", reference,
		"-workers", "1",
		"-batch-size", "10",
		"-verbose",
		"-progress",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", code, stderr.String())
	}
	errOut := stderr.String()
	for _, token := range []string{
		"info: starting run:",
		"progress: hash reference:",
		"progress: hash source:",
	} {
		if !strings.Contains(errOut, token) {
			t.Fatalf("expected stderr to contain %q, got %q", token, errOut)
		}
	}
}

func TestRunInPlaceSuccessAndSummary(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "A")
	mustMkdirAll(t, source)

	mustWriteFile(t, filepath.Join(source, "a.txt"), []byte("hello"))
	mustWriteFile(t, filepath.Join(source, "b.txt"), []byte("hello"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{
		"-source", source,
		"-in-place",
		"-workers", "1",
		"-batch-size", "10",
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	for _, token := range []string{
		"mode: dry-run",
		"scope: in-place",
		"matched in source: 1",
	} {
		if !strings.Contains(out, token) {
			t.Fatalf("expected output to contain %q, got: %s", token, out)
		}
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
