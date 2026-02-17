package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"ultimate-dedup/internal/dedup"
)

type parsedArgs struct {
	cfg  dedup.Config
	help bool
}

func Parse(args []string, out io.Writer) (parsedArgs, error) {
	fs := flag.NewFlagSet("ultimate-dedup", flag.ContinueOnError)
	fs.SetOutput(out)

	source := fs.String("source", "", "source directory A (files are deleted from here)")
	reference := fs.String("reference", "", "reference directory B (read-only reference)")
	batchSize := fs.Int("batch-size", 0, "max deletes per batch; 0 auto")
	workers := fs.Int("workers", 0, "number of hashing and delete workers; 0 auto-tune (~1s)")
	hash := fs.String("hash", dedup.HashXXH3_128, "hash algorithm: xxh3-128|sha256")
	apply := fs.Bool("apply", false, "apply deletions; default is dry-run")
	noVerify := fs.Bool("no-verify", false, "disable byte-by-byte verification (faster, less safe)")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return parsedArgs{help: true}, nil
		}
		return parsedArgs{}, err
	}
	if fs.NArg() != 0 {
		return parsedArgs{}, fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}

	return parsedArgs{
		cfg: dedup.Config{
			SourceDir:        *source,
			ReferenceDir:     *reference,
			BatchSize:        *batchSize,
			Workers:          *workers,
			Hash:             *hash,
			Apply:            *apply,
			Verify:           !*noVerify,
			AutoTuneDuration: time.Second,
		},
	}, nil
}

func Run(args []string, stdout, stderr io.Writer) int {
	parsed, err := Parse(args, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if parsed.help {
		return 0
	}

	stats, err := dedup.Run(parsed.cfg)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	printSummary(stdout, parsed.cfg, stats)
	return 0
}

func printSummary(out io.Writer, cfg dedup.Config, stats dedup.Stats) {
	mode := "dry-run"
	if cfg.Apply {
		mode = "apply"
	}
	fmt.Fprintf(out, "mode: %s\n", mode)
	fmt.Fprintf(out, "hash: %s verify=%t\n", cfg.Hash, cfg.Verify)
	fmt.Fprintf(
		out,
		"workers: %d (auto=%t) batch-size: %d (auto=%t)\n",
		stats.WorkersUsed,
		stats.AutoTunedWorkers,
		stats.BatchSizeUsed,
		stats.AutoTunedBatchSize,
	)
	fmt.Fprintf(out, "scanned: source=%d reference=%d\n", stats.SourceFiles, stats.ReferenceFiles)
	fmt.Fprintf(out, "candidates: source=%d reference=%d\n", stats.CandidateSourceFiles, stats.CandidateReferenceFiles)
	fmt.Fprintf(out, "hashed: source=%d reference=%d\n", stats.HashedSourceFiles, stats.HashedReferenceFiles)
	fmt.Fprintf(out, "matched in source: %d\n", stats.MatchedFiles)
	fmt.Fprintf(out, "reclaimable: %s\n", dedup.FormatBytes(stats.BytesReclaimable))
	if cfg.Apply {
		fmt.Fprintf(
			out,
			"deleted: %d files, reclaimed=%s, delete_errors=%d\n",
			stats.DeletedFiles,
			dedup.FormatBytes(stats.BytesDeleted),
			stats.DeleteErrors,
		)
	}
	fmt.Fprintf(out, "duration: %s\n", stats.Duration.Round(time.Millisecond))
}
