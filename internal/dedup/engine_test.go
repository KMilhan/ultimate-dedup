package dedup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunDeleteSourceFilesThatExistInReferenceByContent(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "A")
	reference := filepath.Join(root, "B")
	mustMkdirAll(t, source)
	mustMkdirAll(t, reference)

	mustWriteFile(t, filepath.Join(reference, "r1.txt"), []byte("hello"))
	mustWriteFile(t, filepath.Join(reference, "r2.txt"), []byte("world"))
	mustWriteFile(t, filepath.Join(reference, "r3.txt"), []byte("abcde"))

	mustWriteFile(t, filepath.Join(source, "s1.txt"), []byte("hello")) // delete
	mustWriteFile(t, filepath.Join(source, "s2.txt"), []byte("world")) // delete
	mustWriteFile(t, filepath.Join(source, "s3.txt"), []byte("other")) // keep
	mustWriteFile(t, filepath.Join(source, "s4.txt"), []byte("fghij")) // same size as r3, different content

	planStats, err := Run(Config{
		SourceDir:    source,
		ReferenceDir: reference,
		BatchSize:    2,
		Workers:      2,
		Hash:         HashXXH3_128,
		Apply:        false,
		Verify:       true,
	})
	if err != nil {
		t.Fatalf("Run dry-run returned error: %v", err)
	}
	if planStats.MatchedFiles != 2 {
		t.Fatalf("expected 2 matched files in dry-run, got %d", planStats.MatchedFiles)
	}
	if planStats.BytesReclaimable != int64(len("hello")+len("world")) {
		t.Fatalf("unexpected reclaimable bytes: %d", planStats.BytesReclaimable)
	}

	for _, p := range []string{
		filepath.Join(source, "s1.txt"),
		filepath.Join(source, "s2.txt"),
		filepath.Join(source, "s3.txt"),
		filepath.Join(source, "s4.txt"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected file to exist after dry-run: %s (%v)", p, err)
		}
	}

	applyStats, err := Run(Config{
		SourceDir:    source,
		ReferenceDir: reference,
		BatchSize:    1,
		Workers:      2,
		Hash:         HashXXH3_128,
		Apply:        true,
		Verify:       true,
	})
	if err != nil {
		t.Fatalf("Run apply returned error: %v", err)
	}
	if applyStats.DeletedFiles != 2 {
		t.Fatalf("expected 2 deleted files, got %d", applyStats.DeletedFiles)
	}
	if applyStats.DeleteErrors != 0 {
		t.Fatalf("expected 0 delete errors, got %d", applyStats.DeleteErrors)
	}

	for _, p := range []string{
		filepath.Join(source, "s1.txt"),
		filepath.Join(source, "s2.txt"),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("expected file to be deleted: %s", p)
		}
	}
	for _, p := range []string{
		filepath.Join(source, "s3.txt"),
		filepath.Join(source, "s4.txt"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected file to remain: %s (%v)", p, err)
		}
	}
}

func TestRunWithSHA256(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "A")
	reference := filepath.Join(root, "B")
	mustMkdirAll(t, source)
	mustMkdirAll(t, reference)

	mustWriteFile(t, filepath.Join(reference, "r1.txt"), []byte("match-me"))
	mustWriteFile(t, filepath.Join(source, "s1.txt"), []byte("match-me"))
	mustWriteFile(t, filepath.Join(source, "s2.txt"), []byte("nope"))

	stats, err := Run(Config{
		SourceDir:    source,
		ReferenceDir: reference,
		BatchSize:    10,
		Workers:      2,
		Hash:         HashSHA256,
		Apply:        false,
		Verify:       true,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if stats.MatchedFiles != 1 {
		t.Fatalf("expected 1 match with sha256, got %d", stats.MatchedFiles)
	}
}

func TestRunAutoTuneWorkersAndBatchWhenUnset(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "A")
	reference := filepath.Join(root, "B")
	mustMkdirAll(t, source)
	mustMkdirAll(t, reference)

	mustWriteFile(t, filepath.Join(reference, "r1.txt"), []byte("alpha"))
	mustWriteFile(t, filepath.Join(source, "s1.txt"), []byte("alpha"))

	stats, err := Run(Config{
		SourceDir:        source,
		ReferenceDir:     reference,
		BatchSize:        0,
		Workers:          0,
		Hash:             HashXXH3_128,
		Apply:            false,
		Verify:           true,
		AutoTuneDuration: 30 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !stats.AutoTunedWorkers {
		t.Fatalf("expected auto-tuned workers")
	}
	if !stats.AutoTunedBatchSize {
		t.Fatalf("expected auto-tuned batch size")
	}
	if stats.WorkersUsed < 1 {
		t.Fatalf("expected workers >= 1, got %d", stats.WorkersUsed)
	}
	if stats.BatchSizeUsed < 1 {
		t.Fatalf("expected batch-size >= 1, got %d", stats.BatchSizeUsed)
	}
	if stats.MatchedFiles != 1 {
		t.Fatalf("expected 1 match, got %d", stats.MatchedFiles)
	}
}

func TestRunAutoTuneFallbackNoReferenceCandidates(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "A")
	reference := filepath.Join(root, "B")
	mustMkdirAll(t, source)
	mustMkdirAll(t, reference)

	mustWriteFile(t, filepath.Join(source, "s1.bin"), []byte("12345"))
	mustWriteFile(t, filepath.Join(reference, "r1.bin"), []byte("x")) // different size from source

	stats, err := Run(Config{
		SourceDir:    source,
		ReferenceDir: reference,
		Workers:      0,
		BatchSize:    0,
		Hash:         HashXXH3_128,
		Verify:       true,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !stats.AutoTunedWorkers || !stats.AutoTunedBatchSize {
		t.Fatalf("expected fallback auto tune for workers/batch-size")
	}
	if stats.HashedReferenceFiles != 0 {
		t.Fatalf("expected no hashing when no ref candidates")
	}
}

func TestRunValidationErrors(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "A")
	b := filepath.Join(root, "B")
	mustMkdirAll(t, a)
	mustMkdirAll(t, b)

	cases := []Config{
		{ReferenceDir: b},
		{SourceDir: a},
		{SourceDir: a, ReferenceDir: b, Workers: -1},
		{SourceDir: a, ReferenceDir: b, BatchSize: -1},
		{SourceDir: a, ReferenceDir: b, Hash: "bad"},
		{SourceDir: a, ReferenceDir: a},
		{SourceDir: a, ReferenceDir: filepath.Join(a, "nested")},
	}
	for i, cfg := range cases {
		if _, err := Run(cfg); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
}

func TestRunKeepsCollisionCheckWhenNoVerifyDisabled(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "A")
	reference := filepath.Join(root, "B")
	mustMkdirAll(t, source)
	mustMkdirAll(t, reference)

	// Intentionally same size, different content.
	mustWriteFile(t, filepath.Join(source, "s1.bin"), []byte("abcde"))
	mustWriteFile(t, filepath.Join(reference, "r1.bin"), []byte("fghij"))

	stats, err := Run(Config{
		SourceDir:    source,
		ReferenceDir: reference,
		Workers:      1,
		BatchSize:    10,
		Hash:         HashSHA256,
		Verify:       true,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if stats.MatchedFiles != 0 {
		t.Fatalf("expected 0 matches, got %d", stats.MatchedFiles)
	}
}

func TestHelpers(t *testing.T) {
	if !pathsOverlap("/tmp/a", "/tmp/a/b") {
		t.Fatalf("expected overlap")
	}
	if pathsOverlap("/tmp/a", "/tmp/b") {
		t.Fatalf("did not expect overlap")
	}
	if got := autoBatchSize(0); got != 2000 {
		t.Fatalf("autoBatchSize(0)=%d", got)
	}
	if got := autoBatchSize(100); got != 50000 {
		t.Fatalf("autoBatchSize(100)=%d", got)
	}
	if got := FormatBytes(123); got != "123 B" {
		t.Fatalf("FormatBytes bytes=%q", got)
	}
	if got := FormatBytes(1024); got == "1024 B" {
		t.Fatalf("FormatBytes expected unit conversion, got %q", got)
	}
	if got := FormatBytes(1024 * 1024); got != "1.0 MiB" {
		t.Fatalf("FormatBytes expected MiB conversion, got %q", got)
	}
}

func TestProbeHelpers(t *testing.T) {
	root := t.TempDir()
	f1 := filepath.Join(root, "f1")
	f2 := filepath.Join(root, "f2")
	mustWriteFile(t, f1, []byte("content-a"))
	mustWriteFile(t, f2, []byte("content-b"))

	files := []fileMeta{
		{Path: f1, Size: 9},
		{Path: f2, Size: 9},
	}
	if got := sampleFilesForProbe(files, 1); len(got) != 1 {
		t.Fatalf("sampleFilesForProbe len=%d", len(got))
	}
	if got := sampleFilesForProbe(files, 10); len(got) != 2 {
		t.Fatalf("sampleFilesForProbe no-limit len=%d", len(got))
	}

	candidates := workerCandidates(1)
	if len(candidates) == 0 || candidates[0] != 1 {
		t.Fatalf("workerCandidates invalid: %#v", candidates)
	}
	if throughput := probeHashThroughput(files, 1, HashXXH3_128, 20*time.Millisecond); throughput <= 0 {
		t.Fatalf("expected positive throughput, got %f", throughput)
	}
}

func TestHashAndCompareErrors(t *testing.T) {
	root := t.TempDir()
	f1 := filepath.Join(root, "f1")
	f2 := filepath.Join(root, "f2")
	mustWriteFile(t, f1, []byte("aa"))
	mustWriteFile(t, f2, []byte("bbb"))

	if _, err := hashFile(f1, "bad"); err == nil {
		t.Fatalf("expected hashFile unsupported algo error")
	}

	eq, err := filesEqual(f1, f2)
	if err != nil {
		t.Fatalf("filesEqual error: %v", err)
	}
	if eq {
		t.Fatalf("filesEqual expected false for different sizes")
	}

	if _, err := filesEqual(filepath.Join(root, "missing-a"), f2); err == nil {
		t.Fatalf("expected filesEqual error when first path missing")
	}
	if _, err := filesEqual(f1, filepath.Join(root, "missing-b")); err == nil {
		t.Fatalf("expected filesEqual error when second path missing")
	}
}

func TestWalkRegularFilesBranches(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file.txt")
	link := filepath.Join(root, "link.txt")
	mustWriteFile(t, file, []byte("x"))
	if err := os.Symlink(file, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	files, err := walkRegularFiles(root)
	if err != nil {
		t.Fatalf("walkRegularFiles error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected only regular file counted, got %d", len(files))
	}
}

func TestWalkRegularFilesError(t *testing.T) {
	_, err := walkRegularFiles(filepath.Join(t.TempDir(), "not-found"))
	if err == nil {
		t.Fatalf("expected walkRegularFiles error for missing root")
	}
}

func TestHashFilesErrorPath(t *testing.T) {
	files := []fileMeta{{Path: "/definitely/missing/file", Size: 123}}
	_, err := hashFiles(files, 1, HashXXH3_128)
	if err == nil {
		t.Fatalf("expected hashFiles error")
	}
}

func TestMatchesAnyFileByContentBranches(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	ref := filepath.Join(root, "ref")
	mustWriteFile(t, source, []byte("aa"))
	mustWriteFile(t, ref, []byte("bb"))

	matched, err := matchesAnyFileByContent(source, nil)
	if err != nil {
		t.Fatalf("unexpected nil-candidates error: %v", err)
	}
	if matched {
		t.Fatalf("expected no match with empty candidates")
	}

	_, err = matchesAnyFileByContent(source, []string{filepath.Join(root, "missing")})
	if err == nil {
		t.Fatalf("expected compare error for missing candidate")
	}
}

func TestRunNoVerifyBranchAndDefaults(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "A")
	reference := filepath.Join(root, "B")
	mustMkdirAll(t, source)
	mustMkdirAll(t, reference)

	mustWriteFile(t, filepath.Join(source, "a.txt"), []byte("same"))
	mustWriteFile(t, filepath.Join(reference, "b.txt"), []byte("same"))

	stats, err := Run(Config{
		SourceDir:    source,
		ReferenceDir: reference,
		Workers:      1,
		BatchSize:    1,
		Hash:         "",
		Verify:       false,
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if stats.MatchedFiles != 1 {
		t.Fatalf("expected match with no verify, got %d", stats.MatchedFiles)
	}
}

func TestAutoTuneWorkersFallbackBranches(t *testing.T) {
	if got := autoTuneWorkers(nil, HashXXH3_128, time.Second); got < 1 {
		t.Fatalf("expected fallback workers >=1, got %d", got)
	}
	root := t.TempDir()
	path := filepath.Join(root, "f")
	mustWriteFile(t, path, []byte("abc"))
	files := []fileMeta{{Path: path, Size: 3}}
	if got := autoTuneWorkers(files, HashXXH3_128, 0); got < 1 {
		t.Fatalf("expected fallback workers >=1 for zero budget, got %d", got)
	}
}

func TestSampleFilesForProbeAllZeroSizes(t *testing.T) {
	files := []fileMeta{
		{Path: "a", Size: 0},
		{Path: "b", Size: 0},
		{Path: "c", Size: 0},
	}
	got := sampleFilesForProbe(files, 2)
	if len(got) == 0 {
		t.Fatalf("expected fallback non-empty sample for zero-size list")
	}
}

func TestWorkerCandidatesZero(t *testing.T) {
	got := workerCandidates(0)
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("expected [1], got %#v", got)
	}
}

func TestIsSubpathErrorBranch(t *testing.T) {
	// filepath.Rel returns error when mixing absolute and relative paths.
	if isSubpath("/tmp/base", "relative/path") {
		t.Fatalf("expected false for mixed abs/rel inputs")
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
