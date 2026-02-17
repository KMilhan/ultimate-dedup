package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ultimate-dedup/internal/cli"
)

type fixtureFile struct {
	RelPath string
	Data    []byte
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "E2E failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("E2E passed")
}

func run() error {
	root, err := os.MkdirTemp("", "ultimate-dedup-e2e-*")
	if err != nil {
		return fmt.Errorf("create temp root: %w", err)
	}
	keep := os.Getenv("E2E_KEEP") == "1"
	if !keep {
		defer os.RemoveAll(root)
	}
	fmt.Printf("workspace: %s (keep=%t)\n", root, keep)

	sourceDir := filepath.Join(root, "source")
	targetDir := filepath.Join(root, "target")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		return fmt.Errorf("create source dir: %w", err)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("create target dir: %w", err)
	}

	sourceFiles, targetFiles, expectedDeleted, expectedKept := buildFixtures()
	if err := writeFixtures(sourceDir, sourceFiles); err != nil {
		return fmt.Errorf("write source fixtures: %w", err)
	}
	if err := writeFixtures(targetDir, targetFiles); err != nil {
		return fmt.Errorf("write target fixtures: %w", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := cli.Run([]string{
		"-source", sourceDir,
		"-reference", targetDir,
		"-apply",
	}, &stdout, &stderr)

	fmt.Println("----- CLI STDOUT -----")
	fmt.Print(stdout.String())
	if stderr.Len() > 0 {
		fmt.Println("----- CLI STDERR -----")
		fmt.Print(stderr.String())
	}

	if code != 0 {
		return fmt.Errorf("cli exited with code %d", code)
	}

	if !strings.Contains(stdout.String(), fmt.Sprintf("deleted: %d files", len(expectedDeleted))) {
		return fmt.Errorf("unexpected delete count in output, expected %d", len(expectedDeleted))
	}

	if err := verifyDeleted(sourceDir, expectedDeleted); err != nil {
		return err
	}
	if err := verifyPresent(sourceDir, expectedKept); err != nil {
		return err
	}
	if err := verifyPresent(targetDir, relPaths(targetFiles)); err != nil {
		return fmt.Errorf("target directory should stay unchanged: %w", err)
	}

	return nil
}

func buildFixtures() (source []fixtureFile, target []fixtureFile, expectedDeleted []string, expectedKept []string) {
	largeSourceDuplicate := bytes.Repeat([]byte("L"), 1024*1024)       // 1 MiB
	mediumSourceDuplicate := bytes.Repeat([]byte("M"), 128*1024)       // 128 KiB
	mediumTargetDifferent := bytes.Repeat([]byte("N"), 128*1024)       // 128 KiB
	largeUniqueSource := bytes.Repeat([]byte("U"), 1024*1024+17)       // ~1 MiB
	mediumUniqueTarget := bytes.Repeat([]byte("T"), 256*1024+3)        // 256 KiB
	sameNameDifferentA := []byte("source-side-same-name-content")      // same name, different bytes
	sameNameDifferentB := []byte("target-side-same-name-content-diff") // same name, different bytes

	source = []fixtureFile{
		{RelPath: "nested/one/same-content-different-name.txt", Data: []byte("small-dup-1")},
		{RelPath: "nested/two/another-name.log", Data: []byte("small-dup-2")},
		{RelPath: "nested/two/large.bin", Data: largeSourceDuplicate},
		{RelPath: "nested/three/medium.dat", Data: mediumSourceDuplicate},
		{RelPath: "nested/four/same-name-different-content.txt", Data: sameNameDifferentA},
		{RelPath: "nested/five/unique-large.bin", Data: largeUniqueSource},
		{RelPath: "nested/five/same-name-same-content.txt", Data: []byte("same-name-same-content")},
		{RelPath: "nested/six/unique-small.txt", Data: []byte("source-only")},
	}

	target = []fixtureFile{
		// Same content, different name/path.
		{RelPath: "archive/x/renamed-a.txt", Data: []byte("small-dup-1")},
		{RelPath: "archive/y/renamed-b.bin", Data: []byte("small-dup-2")},
		{RelPath: "archive/z/another-large-name.bin", Data: largeSourceDuplicate},
		{RelPath: "archive/z2/another-medium-name.bin", Data: mediumSourceDuplicate},

		// Same name, different content (must not delete source counterpart).
		{RelPath: "nested/four/same-name-different-content.txt", Data: sameNameDifferentB},

		// Same name, same content (should delete source file).
		{RelPath: "nested/five/same-name-same-content.txt", Data: []byte("same-name-same-content")},

		// Target-only noise.
		{RelPath: "archive/noise/medium-target-only.bin", Data: mediumUniqueTarget},
		{RelPath: "archive/noise/medium-same-size-different-content.dat", Data: mediumTargetDifferent},
	}

	expectedDeleted = []string{
		"nested/one/same-content-different-name.txt",
		"nested/two/another-name.log",
		"nested/two/large.bin",
		"nested/three/medium.dat",
		"nested/five/same-name-same-content.txt",
	}

	expectedKept = []string{
		"nested/four/same-name-different-content.txt",
		"nested/five/unique-large.bin",
		"nested/six/unique-small.txt",
	}

	return source, target, expectedDeleted, expectedKept
}

func writeFixtures(root string, files []fixtureFile) error {
	for _, f := range files {
		full := filepath.Join(root, f.RelPath)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, f.Data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", full, err)
		}
	}
	return nil
}

func relPaths(files []fixtureFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.RelPath)
	}
	return out
}

func verifyDeleted(root string, rels []string) error {
	for _, rel := range rels {
		path := filepath.Join(root, rel)
		_, err := os.Stat(path)
		if err == nil {
			return fmt.Errorf("expected deleted but still exists: %s", rel)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("unexpected stat error for %s: %w", rel, err)
		}
	}
	return nil
}

func verifyPresent(root string, rels []string) error {
	for _, rel := range rels {
		path := filepath.Join(root, rel)
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("expected present file %s: %w", rel, err)
		}
	}
	return nil
}
