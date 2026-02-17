package dedup

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zeebo/xxh3"
)

const (
	HashXXH3_128 = "xxh3-128"
	HashSHA256   = "sha256"
)

const defaultAutoTuneDuration = time.Second

type Config struct {
	SourceDir        string
	ReferenceDir     string
	InPlace          bool
	BatchSize        int
	Workers          int
	Hash             string
	Apply            bool
	Verify           bool
	Verbose          bool
	Progress         bool
	Logf             func(format string, args ...any)
	AutoTuneDuration time.Duration
}

type Stats struct {
	SourceFiles             int
	ReferenceFiles          int
	CandidateSourceFiles    int
	CandidateReferenceFiles int
	WorkersUsed             int
	BatchSizeUsed           int
	AutoTunedWorkers        bool
	AutoTunedBatchSize      bool
	HashedSourceFiles       int
	HashedReferenceFiles    int
	MatchedFiles            int
	BytesReclaimable        int64
	DeletedFiles            int
	BytesDeleted            int64
	DeleteErrors            int
	Duration                time.Duration
}

type fileMeta struct {
	Path string
	Size int64
}

type fileHash struct {
	Meta fileMeta
	Sum  [32]byte
	Err  error
}

type hashKey struct {
	Size int64
	Sum  [32]byte
}

type deleteItem struct {
	Path string
	Size int64
}

var hashBufferPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 1024*1024)
		return &buf
	},
}

func (cfg Config) verbosef(format string, args ...any) {
	if cfg.Verbose && cfg.Logf != nil {
		cfg.Logf("info: "+format, args...)
	}
}

func (cfg Config) progressf(format string, args ...any) {
	if cfg.Progress && cfg.Logf != nil {
		cfg.Logf("progress: "+format, args...)
	}
}

type progressReporter struct {
	cfg      Config
	label    string
	total    int
	started  time.Time
	lastEmit time.Time
}

func newProgressReporter(cfg Config, label string, total int) *progressReporter {
	if !cfg.Progress || cfg.Logf == nil || total <= 0 {
		return nil
	}
	cfg.progressf("%s: 0/%d (0%%)", label, total)
	now := time.Now()
	return &progressReporter{
		cfg:      cfg,
		label:    label,
		total:    total,
		started:  now,
		lastEmit: now,
	}
}

func (p *progressReporter) step(done int) {
	if p == nil {
		return
	}
	if done < 0 {
		done = 0
	}
	if done > p.total {
		done = p.total
	}
	now := time.Now()
	if done != p.total && now.Sub(p.lastEmit) < 500*time.Millisecond {
		return
	}
	p.lastEmit = now
	percent := float64(done) * 100 / float64(p.total)
	p.cfg.progressf(
		"%s: %d/%d (%.0f%% elapsed=%s)",
		p.label,
		done,
		p.total,
		percent,
		time.Since(p.started).Round(time.Millisecond),
	)
}

func Run(cfg Config) (Stats, error) {
	start := time.Now()
	var stats Stats

	if cfg.Workers < 0 {
		return stats, errors.New("--workers must be >= 0")
	}
	if cfg.BatchSize < 0 {
		return stats, errors.New("--batch-size must be >= 0")
	}
	if cfg.Hash == "" {
		cfg.Hash = HashXXH3_128
	}
	if cfg.Hash != HashXXH3_128 && cfg.Hash != HashSHA256 {
		return stats, fmt.Errorf("--hash must be one of: %s, %s", HashXXH3_128, HashSHA256)
	}
	if cfg.AutoTuneDuration <= 0 {
		cfg.AutoTuneDuration = defaultAutoTuneDuration
	}
	cfg.verbosef(
		"starting run: mode=%s apply=%t verify=%t hash=%s",
		func() string {
			if cfg.InPlace {
				return "in-place"
			}
			return "source-vs-reference"
		}(),
		cfg.Apply,
		cfg.Verify,
		cfg.Hash,
	)

	if cfg.InPlace {
		if cfg.SourceDir == "" {
			return stats, errors.New("--source is required when --in-place is set")
		}
		if cfg.ReferenceDir != "" {
			return stats, errors.New("--reference must be empty when --in-place is set")
		}
		return runInPlace(cfg, start)
	}
	if cfg.SourceDir == "" || cfg.ReferenceDir == "" {
		return stats, errors.New("both --source and --reference are required")
	}

	sourceAbs, err := filepath.Abs(cfg.SourceDir)
	if err != nil {
		return stats, fmt.Errorf("resolve source path: %w", err)
	}
	refAbs, err := filepath.Abs(cfg.ReferenceDir)
	if err != nil {
		return stats, fmt.Errorf("resolve reference path: %w", err)
	}
	sourceAbs = filepath.Clean(sourceAbs)
	refAbs = filepath.Clean(refAbs)

	if sourceAbs == refAbs {
		return stats, errors.New("source and reference must be different directories")
	}
	if pathsOverlap(sourceAbs, refAbs) {
		return stats, errors.New("source and reference must not overlap")
	}

	cfg.verbosef("scanning source: %s", sourceAbs)
	sourceFiles, err := walkRegularFiles(sourceAbs)
	if err != nil {
		return stats, err
	}
	stats.SourceFiles = len(sourceFiles)
	cfg.verbosef("scanned source files: %d", stats.SourceFiles)

	sourceSizes := make(map[int64]struct{}, len(sourceFiles))
	for _, f := range sourceFiles {
		sourceSizes[f.Size] = struct{}{}
	}

	cfg.verbosef("scanning reference: %s", refAbs)
	referenceFiles, err := walkRegularFiles(refAbs)
	if err != nil {
		return stats, err
	}
	stats.ReferenceFiles = len(referenceFiles)
	cfg.verbosef("scanned reference files: %d", stats.ReferenceFiles)

	refCandidates := filterBySize(referenceFiles, sourceSizes)
	stats.CandidateReferenceFiles = len(refCandidates)
	cfg.verbosef("reference candidates by size: %d", stats.CandidateReferenceFiles)
	if len(refCandidates) == 0 {
		if cfg.Workers == 0 {
			cfg.Workers = runtime.NumCPU()
			if cfg.Workers < 1 {
				cfg.Workers = 1
			}
			stats.AutoTunedWorkers = true
		}
		if cfg.BatchSize == 0 {
			cfg.BatchSize = autoBatchSize(cfg.Workers)
			stats.AutoTunedBatchSize = true
		}
		stats.WorkersUsed = cfg.Workers
		stats.BatchSizeUsed = cfg.BatchSize
		stats.Duration = time.Since(start)
		cfg.verbosef("no reference candidates found; finishing early")
		return stats, nil
	}

	if cfg.Workers == 0 {
		cfg.verbosef("auto-tuning workers")
		cfg.Workers = autoTuneWorkers(refCandidates, cfg.Hash, cfg.AutoTuneDuration)
		stats.AutoTunedWorkers = true
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = autoBatchSize(cfg.Workers)
		stats.AutoTunedBatchSize = true
	}
	stats.WorkersUsed = cfg.Workers
	stats.BatchSizeUsed = cfg.BatchSize
	cfg.verbosef("using workers=%d batch-size=%d", stats.WorkersUsed, stats.BatchSizeUsed)

	cfg.verbosef("hashing reference candidates")
	refProgress := newProgressReporter(cfg, "hash reference", len(refCandidates))
	refHashed, err := hashFiles(refCandidates, cfg.Workers, cfg.Hash, refProgress.step)
	if err != nil {
		return stats, err
	}
	stats.HashedReferenceFiles = len(refHashed)
	cfg.verbosef("hashed reference candidates: %d", stats.HashedReferenceFiles)

	refIndex := make(map[hashKey][]string, len(refHashed))
	refHashedSizes := make(map[int64]struct{})
	for _, h := range refHashed {
		key := hashKey{Size: h.Meta.Size, Sum: h.Sum}
		refIndex[key] = append(refIndex[key], h.Meta.Path)
		refHashedSizes[h.Meta.Size] = struct{}{}
	}

	sourceCandidates := filterBySize(sourceFiles, refHashedSizes)
	stats.CandidateSourceFiles = len(sourceCandidates)
	cfg.verbosef("source candidates by size: %d", stats.CandidateSourceFiles)
	if len(sourceCandidates) == 0 {
		stats.Duration = time.Since(start)
		cfg.verbosef("no source candidates found; finishing early")
		return stats, nil
	}

	cfg.verbosef("hashing source candidates")
	sourceProgress := newProgressReporter(cfg, "hash source", len(sourceCandidates))
	sourceHashed, err := hashFiles(sourceCandidates, cfg.Workers, cfg.Hash, sourceProgress.step)
	if err != nil {
		return stats, err
	}
	stats.HashedSourceFiles = len(sourceHashed)
	cfg.verbosef("hashed source candidates: %d", stats.HashedSourceFiles)

	toDelete := make([]deleteItem, 0)
	for _, s := range sourceHashed {
		key := hashKey{Size: s.Meta.Size, Sum: s.Sum}
		refPaths, ok := refIndex[key]
		if !ok {
			continue
		}
		if cfg.Verify {
			matched, err := matchesAnyFileByContent(s.Meta.Path, refPaths)
			if err != nil {
				return stats, err
			}
			if !matched {
				continue
			}
		}
		toDelete = append(toDelete, deleteItem{Path: s.Meta.Path, Size: s.Meta.Size})
		stats.MatchedFiles++
		stats.BytesReclaimable += s.Meta.Size
	}
	cfg.verbosef(
		"matched source files: %d reclaimable=%s",
		stats.MatchedFiles,
		FormatBytes(stats.BytesReclaimable),
	)

	if cfg.Apply && len(toDelete) > 0 {
		cfg.verbosef("deleting matched files: %d", len(toDelete))
		deleteProgress := newProgressReporter(cfg, "delete", len(toDelete))
		deleted, bytesDeleted, deleteErrors := deleteInBatches(toDelete, cfg.BatchSize, cfg.Workers, deleteProgress.step)
		stats.DeletedFiles = deleted
		stats.BytesDeleted = bytesDeleted
		stats.DeleteErrors = deleteErrors
		cfg.verbosef(
			"delete completed: deleted=%d errors=%d reclaimed=%s",
			stats.DeletedFiles,
			stats.DeleteErrors,
			FormatBytes(stats.BytesDeleted),
		)
	} else if cfg.Apply {
		cfg.verbosef("no files to delete")
	}

	stats.Duration = time.Since(start)
	cfg.verbosef("finished in %s", stats.Duration.Round(time.Millisecond))
	return stats, nil
}

func runInPlace(cfg Config, start time.Time) (Stats, error) {
	var stats Stats

	sourceAbs, err := filepath.Abs(cfg.SourceDir)
	if err != nil {
		return stats, fmt.Errorf("resolve source path: %w", err)
	}
	sourceAbs = filepath.Clean(sourceAbs)

	cfg.verbosef("scanning source: %s", sourceAbs)
	sourceFiles, err := walkRegularFiles(sourceAbs)
	if err != nil {
		return stats, err
	}
	stats.SourceFiles = len(sourceFiles)
	cfg.verbosef("scanned source files: %d", stats.SourceFiles)

	sizeCounts := make(map[int64]int, len(sourceFiles))
	for _, f := range sourceFiles {
		sizeCounts[f.Size]++
	}
	duplicateSizes := make(map[int64]struct{}, len(sizeCounts))
	for size, count := range sizeCounts {
		if count > 1 {
			duplicateSizes[size] = struct{}{}
		}
	}

	sourceCandidates := filterBySize(sourceFiles, duplicateSizes)
	stats.CandidateSourceFiles = len(sourceCandidates)
	cfg.verbosef("source candidates by size: %d", stats.CandidateSourceFiles)

	if cfg.Workers == 0 {
		if len(sourceCandidates) > 0 {
			cfg.verbosef("auto-tuning workers")
			cfg.Workers = autoTuneWorkers(sourceCandidates, cfg.Hash, cfg.AutoTuneDuration)
		} else {
			cfg.Workers = runtime.NumCPU()
			if cfg.Workers < 1 {
				cfg.Workers = 1
			}
		}
		stats.AutoTunedWorkers = true
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = autoBatchSize(cfg.Workers)
		stats.AutoTunedBatchSize = true
	}
	stats.WorkersUsed = cfg.Workers
	stats.BatchSizeUsed = cfg.BatchSize
	cfg.verbosef("using workers=%d batch-size=%d", stats.WorkersUsed, stats.BatchSizeUsed)

	if len(sourceCandidates) == 0 {
		stats.Duration = time.Since(start)
		cfg.verbosef("no candidates found; finishing early")
		return stats, nil
	}

	cfg.verbosef("hashing source candidates")
	sourceProgress := newProgressReporter(cfg, "hash source", len(sourceCandidates))
	sourceHashed, err := hashFiles(sourceCandidates, cfg.Workers, cfg.Hash, sourceProgress.step)
	if err != nil {
		return stats, err
	}
	stats.HashedSourceFiles = len(sourceHashed)
	cfg.verbosef("hashed source candidates: %d", stats.HashedSourceFiles)

	groups := make(map[hashKey][]fileMeta, len(sourceHashed))
	for _, h := range sourceHashed {
		key := hashKey{Size: h.Meta.Size, Sum: h.Sum}
		groups[key] = append(groups[key], h.Meta)
	}

	toDelete := make([]deleteItem, 0)
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		sort.Slice(group, func(i, j int) bool {
			return group[i].Path < group[j].Path
		})
		keep := group[0]
		for _, candidate := range group[1:] {
			if cfg.Verify {
				eq, err := filesEqual(candidate.Path, keep.Path)
				if err != nil {
					return stats, fmt.Errorf("compare %s and %s: %w", candidate.Path, keep.Path, err)
				}
				if !eq {
					continue
				}
			}
			toDelete = append(toDelete, deleteItem{Path: candidate.Path, Size: candidate.Size})
			stats.MatchedFiles++
			stats.BytesReclaimable += candidate.Size
		}
	}
	cfg.verbosef(
		"matched source files: %d reclaimable=%s",
		stats.MatchedFiles,
		FormatBytes(stats.BytesReclaimable),
	)

	if cfg.Apply && len(toDelete) > 0 {
		cfg.verbosef("deleting matched files: %d", len(toDelete))
		deleteProgress := newProgressReporter(cfg, "delete", len(toDelete))
		deleted, bytesDeleted, deleteErrors := deleteInBatches(toDelete, cfg.BatchSize, cfg.Workers, deleteProgress.step)
		stats.DeletedFiles = deleted
		stats.BytesDeleted = bytesDeleted
		stats.DeleteErrors = deleteErrors
		cfg.verbosef(
			"delete completed: deleted=%d errors=%d reclaimed=%s",
			stats.DeletedFiles,
			stats.DeleteErrors,
			FormatBytes(stats.BytesDeleted),
		)
	} else if cfg.Apply {
		cfg.verbosef("no files to delete")
	}

	stats.Duration = time.Since(start)
	cfg.verbosef("finished in %s", stats.Duration.Round(time.Millisecond))
	return stats, nil
}

func walkRegularFiles(root string) ([]fileMeta, error) {
	files := make([]fileMeta, 0)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", path, err)
		}
		files = append(files, fileMeta{Path: path, Size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}
	return files, nil
}

func filterBySize(files []fileMeta, sizes map[int64]struct{}) []fileMeta {
	out := make([]fileMeta, 0, len(files))
	for _, f := range files {
		if _, ok := sizes[f.Size]; ok {
			out = append(out, f)
		}
	}
	return out
}

func hashFiles(files []fileMeta, workers int, algorithm string, onProgress func(done int)) ([]fileHash, error) {
	if len(files) == 0 {
		return nil, nil
	}
	jobs := make(chan fileMeta)
	results := make(chan fileHash, workers*2)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				sum, err := hashFile(f.Path, algorithm)
				results <- fileHash{
					Meta: f,
					Sum:  sum,
					Err:  err,
				}
			}
		}()
	}

	go func() {
		for _, f := range files {
			jobs <- f
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	out := make([]fileHash, 0, len(files))
	var firstErr error
	done := 0
	for r := range results {
		done++
		if onProgress != nil {
			onProgress(done)
		}
		if r.Err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("hash %s: %w", r.Meta.Path, r.Err)
			}
			continue
		}
		out = append(out, r)
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

func hashFile(path, algorithm string) ([32]byte, error) {
	var out [32]byte
	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer f.Close()

	buf := hashBufferPool.Get().(*[]byte)
	defer hashBufferPool.Put(buf)

	switch algorithm {
	case HashXXH3_128:
		h := xxh3.New128()
		if _, err := io.CopyBuffer(h, f, *buf); err != nil {
			return out, err
		}
		sum := h.Sum128().Bytes()
		copy(out[:16], sum[:])
	case HashSHA256:
		h := sha256.New()
		if _, err := io.CopyBuffer(h, f, *buf); err != nil {
			return out, err
		}
		copy(out[:], h.Sum(nil))
	default:
		return out, fmt.Errorf("unsupported hash algorithm: %s", algorithm)
	}
	return out, nil
}

func matchesAnyFileByContent(path string, candidates []string) (bool, error) {
	for _, c := range candidates {
		eq, err := filesEqual(path, c)
		if err != nil {
			return false, fmt.Errorf("compare %s and %s: %w", path, c, err)
		}
		if eq {
			return true, nil
		}
	}
	return false, nil
}

func filesEqual(aPath, bPath string) (bool, error) {
	aInfo, err := os.Stat(aPath)
	if err != nil {
		return false, err
	}
	bInfo, err := os.Stat(bPath)
	if err != nil {
		return false, err
	}
	if aInfo.Size() != bInfo.Size() {
		return false, nil
	}

	a, err := os.Open(aPath)
	if err != nil {
		return false, err
	}
	defer a.Close()
	b, err := os.Open(bPath)
	if err != nil {
		return false, err
	}
	defer b.Close()

	bufA := make([]byte, 1024*1024)
	bufB := make([]byte, 1024*1024)

	for {
		nA, errA := a.Read(bufA)
		nB, errB := b.Read(bufB)
		if nA != nB {
			return false, nil
		}
		if !bytes.Equal(bufA[:nA], bufB[:nB]) {
			return false, nil
		}
		if errA == io.EOF && errB == io.EOF {
			return true, nil
		}
		if errA != nil && errA != io.EOF {
			return false, errA
		}
		if errB != nil && errB != io.EOF {
			return false, errB
		}
	}
}

func deleteInBatches(items []deleteItem, batchSize, workers int, onProgress func(done int)) (int, int64, int) {
	totalDeleted := 0
	var totalBytes int64
	totalErrors := 0
	processed := 0

	for i := 0; i < len(items); i += batchSize {
		end := i + batchSize
		if end > len(items) {
			end = len(items)
		}
		deleted, bytesDeleted, errs := deleteBatch(items[i:end], workers, func() {
			processed++
			if onProgress != nil {
				onProgress(processed)
			}
		})
		totalDeleted += deleted
		totalBytes += bytesDeleted
		totalErrors += errs
	}
	return totalDeleted, totalBytes, totalErrors
}

func deleteBatch(items []deleteItem, workers int, onItemProcessed func()) (int, int64, int) {
	type result struct {
		item deleteItem
		err  error
	}

	jobs := make(chan deleteItem)
	results := make(chan result, workers*2)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				err := os.Remove(item.Path)
				results <- result{item: item, err: err}
			}
		}()
	}

	go func() {
		for _, item := range items {
			jobs <- item
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	deleted := 0
	var bytesDeleted int64
	deleteErrors := 0
	for r := range results {
		if onItemProcessed != nil {
			onItemProcessed()
		}
		if r.err != nil {
			deleteErrors++
			continue
		}
		deleted++
		bytesDeleted += r.item.Size
	}
	return deleted, bytesDeleted, deleteErrors
}

func pathsOverlap(a, b string) bool {
	return isSubpath(a, b) || isSubpath(b, a)
}

func isSubpath(base, candidate string) bool {
	rel, err := filepath.Rel(base, candidate)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return false
	}
	return true
}

func autoTuneWorkers(files []fileMeta, algorithm string, budget time.Duration) int {
	maxWorkers := runtime.NumCPU()
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	if len(files) == 0 {
		return maxWorkers
	}
	if budget <= 0 {
		return maxWorkers
	}

	samples := sampleFilesForProbe(files, 24)
	if len(samples) == 0 {
		return maxWorkers
	}

	candidates := workerCandidates(maxWorkers)
	if len(candidates) == 0 {
		return maxWorkers
	}
	perCandidate := budget / time.Duration(len(candidates))
	if perCandidate <= 0 {
		perCandidate = 50 * time.Millisecond
	}

	bestWorkers := candidates[0]
	bestThroughput := float64(-1)
	for _, workers := range candidates {
		throughput := probeHashThroughput(samples, workers, algorithm, perCandidate)
		// Prefer higher worker counts when throughput ties, avoiding a persistent
		// bias toward the first candidate (1 worker).
		if throughput > bestThroughput || (throughput == bestThroughput && workers > bestWorkers) {
			bestThroughput = throughput
			bestWorkers = workers
		}
	}
	if bestWorkers < 1 {
		return 1
	}
	return bestWorkers
}

func sampleFilesForProbe(files []fileMeta, limit int) []fileMeta {
	if limit <= 0 || len(files) <= limit {
		return files
	}
	step := len(files) / limit
	if step < 1 {
		step = 1
	}
	out := make([]fileMeta, 0, limit)
	for i := 0; i < len(files) && len(out) < limit; i += step {
		if files[i].Size == 0 {
			continue
		}
		out = append(out, files[i])
	}
	if len(out) == 0 {
		for i := 0; i < len(files) && len(out) < limit; i += step {
			out = append(out, files[i])
		}
	}
	return out
}

func workerCandidates(maxWorkers int) []int {
	if maxWorkers < 1 {
		return []int{1}
	}
	half := maxWorkers / 2
	if half < 1 {
		half = 1
	}
	double := maxWorkers * 2
	if double > 128 {
		double = 128
	}
	seen := map[int]struct{}{}
	out := make([]int, 0, 4)
	for _, v := range []int{1, half, maxWorkers, double} {
		if v < 1 {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func probeHashThroughput(samples []fileMeta, workers int, algorithm string, duration time.Duration) float64 {
	if workers < 1 || len(samples) == 0 || duration <= 0 {
		return 0
	}
	var bytesProcessed atomic.Int64
	deadline := time.Now().Add(duration)

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(workerIdx int) {
			defer wg.Done()
			var localBytes int64
			sampleIdx := workerIdx % len(samples)
			for time.Now().Before(deadline) {
				f := samples[sampleIdx]
				sampleIdx++
				if sampleIdx >= len(samples) {
					sampleIdx = 0
				}
				_, err := hashFile(f.Path, algorithm)
				if err == nil {
					localBytes += f.Size
				}
			}
			if localBytes > 0 {
				bytesProcessed.Add(localBytes)
			}
		}(i)
	}
	wg.Wait()

	seconds := duration.Seconds()
	if seconds == 0 {
		return 0
	}
	return float64(bytesProcessed.Load()) / seconds
}

func autoBatchSize(workers int) int {
	if workers < 1 {
		workers = 1
	}
	size := workers * 2000
	if size < 1000 {
		return 1000
	}
	if size > 50000 {
		return 50000
	}
	return size
}

func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for value := n / unit; value >= unit; value /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
