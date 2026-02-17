package cli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"ultimate-dedup/internal/dedup"
)

type parsedArgs struct {
	cfg  dedup.Config
	help bool
}

type flagValues struct {
	source    *string
	reference *string
	inPlace   *bool
	batchSize *int
	workers   *int
	hash      *string
	apply     *bool
	noVerify  *bool
	verbose   *bool
	progress  *bool
}

func newFlagSet(out io.Writer) (*flag.FlagSet, flagValues) {
	fs := flag.NewFlagSet("ultimate-dedup", flag.ContinueOnError)
	fs.SetOutput(out)

	values := flagValues{
		source:    fs.String("source", "", "source directory A (files are deleted from here)"),
		reference: fs.String("reference", "", "reference directory B (read-only reference)"),
		inPlace:   fs.Bool("in-place", false, "deduplicate within source only; keep one file per content group"),
		batchSize: fs.Int("batch-size", 0, "max deletes per batch; 0 auto"),
		workers:   fs.Int("workers", 0, "number of hashing and delete workers; 0 auto-tune (~1s)"),
		hash:      fs.String("hash", dedup.HashXXH3_128, "hash algorithm: xxh3-128|sha256"),
		apply:     fs.Bool("apply", false, "apply deletions; default is dry-run"),
		noVerify:  fs.Bool("no-verify", false, "disable byte-by-byte verification (faster, less safe)"),
		verbose:   fs.Bool("verbose", false, "enable basic phase logs on stderr"),
		progress:  fs.Bool("progress", false, "show progress updates for hashing and deletes"),
	}

	fs.Usage = func() {
		fmt.Fprintf(out, "Usage of %s:\n", fs.Name())
		printGNUStyleDefaults(fs, out)
	}

	return fs, values
}

func printGNUStyleDefaults(fs *flag.FlagSet, out io.Writer) {
	originalOut := fs.Output()
	var buf bytes.Buffer
	fs.SetOutput(&buf)
	fs.PrintDefaults()
	fs.SetOutput(originalOut)

	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if strings.HasPrefix(line, "  -") {
			line = "  --" + strings.TrimPrefix(line, "  -")
		}
		fmt.Fprintln(out, line)
	}
}

func Parse(args []string, out io.Writer) (parsedArgs, error) {
	fs, values := newFlagSet(out)

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
			SourceDir:        *values.source,
			ReferenceDir:     *values.reference,
			InPlace:          *values.inPlace,
			BatchSize:        *values.batchSize,
			Workers:          *values.workers,
			Hash:             *values.hash,
			Apply:            *values.apply,
			Verify:           !*values.noVerify,
			Verbose:          *values.verbose,
			Progress:         *values.progress,
			AutoTuneDuration: time.Second,
		},
	}, nil
}

func printNoArgsHelp(out io.Writer) {
	fmt.Fprintln(out, "ultimate-dedup removes files by content match, not by name/path.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Modes:")
	fmt.Fprintln(out, "  source-vs-reference: --source <dir> --reference <dir>")
	fmt.Fprintln(out, "  in-place:            --source <dir> --in-place")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Safety defaults: dry-run + byte verification.")
	fmt.Fprintln(out, "Add --apply to actually delete files.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Examples:")
	fmt.Fprintln(out, "  ultimate-dedup --source /data/A --reference /data/B")
	fmt.Fprintln(out, "  ultimate-dedup --source /data/A --in-place --apply")
	fmt.Fprintln(out)

	fs, _ := newFlagSet(out)
	fs.Usage()
}

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printNoArgsHelp(stderr)
		return 1
	}

	parsed, err := Parse(args, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if parsed.help {
		return 0
	}

	parsed.cfg.Logf = func(format string, args ...any) {
		fmt.Fprintf(stderr, format+"\n", args...)
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
	scope := "source-vs-reference"
	if cfg.InPlace {
		scope = "in-place"
	}
	fmt.Fprintf(out, "mode: %s\n", mode)
	fmt.Fprintf(out, "scope: %s\n", scope)
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
