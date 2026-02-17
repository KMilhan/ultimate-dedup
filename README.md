# ultimate-dedup

`ultimate-dedup` is a CLI program that deletes files in a source directory when identical content exists in a reference directory.

It is content-based only:
- File name does not matter.
- Path does not matter.
- Metadata does not matter.
- ***Bits*** must match.

## What It Does

> Oh, it's already there? We don't need this.

*(It also supports in-the-same directory dedup!)*

Ultimate Dedup does this really fast.

Given:
- Source directory `A` (mutable)
- Reference directory `B` (read-only)

The tool:
1. Check optimal performance
2. Scans both trees recursively.
3. Filters candidates by file size.
4. Hashes candidates (`xxh3-128` by default, or `sha256`).
5. Optionally verifies bytes (`on` by default).
6. Deletes matching files only from `A`.

With `--in-place`:
1. Scans `A` recursively.
2. Groups by size and content hash.
3. Keeps one deterministic file per content group.
4. Deletes the other duplicates from `A`.

## Safety Defaults

- Default mode is dry-run (no deletion) unless `--apply` is set.
- Byte-by-byte verification is enabled by default.
- Source and reference must be different and non-overlapping.
- If `--workers` or `--batch-size` is not provided (or set to `0`), values are auto-tuned.

## Quick Start

Build:

```bash
go build -o ultimate-dedup .
```

Dry-run:

```bash
./ultimate-dedup \
  --source /path/to/source \
  --reference /path/to/target
```

Apply deletion:

```bash
./ultimate-dedup \
  --source /path/to/source \
  --reference /path/to/target \
  --apply
```

In-place dedup (single directory):

```bash
./ultimate-dedup \
  --source /path/to/dir \
  --in-place \
  --apply
```

## CLI Flags

```text
--source string
    source directory A (files are deleted from here)
--reference string
    reference directory B (read-only reference)
--in-place
    deduplicate within source only; keep one file per content group
--apply
    apply deletions; default is dry-run
--hash string
    hash algorithm: xxh3-128|sha256 (default "xxh3-128")
--no-verify
    disable byte-by-byte verification (faster, less safe)
--verbose
    enable basic phase logs on stderr
--progress
    show progress updates for hashing and deletes
--workers int
    number of hashing and delete workers; 0 auto-tune (~1s)
--batch-size int
    max deletes per batch; 0 auto
```

Notes:
- Default mode (without `--in-place`) requires both `--source` and `--reference`.
- In in-place mode (`--in-place`), `--reference` must not be provided.
- `--verbose` and `--progress` write logs to `stderr`; summary output remains on `stdout`.

## Examples

Use explicit tuning:

```bash
./ultimate-dedup \
  --source /data/source \
  --reference /data/reference \
  --workers 16 \
  --batch-size 5000 \
  --apply
```

Use SHA-256:

```bash
./ultimate-dedup \
  --source /data/source \
  --reference /data/reference \
  --hash sha256 \
  --apply
```

## End-to-End Scenario Runner

An executable E2E program is included at `cmd/e2e/main.go`.

It:
1. Creates nested source/target fixtures with mixed sizes.
2. Includes both:
   - same name, different content
   - same content, different name
3. Runs the CLI in apply mode.
4. Verifies source duplicates are deleted and non-duplicates remain.

Run:

```bash
go run ./cmd/e2e
```

Keep temp workspace for inspection:

```bash
E2E_KEEP=1 go run ./cmd/e2e
```

## Development

Format, vet, test:

```bash
go fmt ./...
go vet ./...
go test ./...
```

Coverage:

```bash
go test ./... -coverprofile=coverage.out
go tool cover -func=coverage.out
```

## Project Layout

```text
main.go                    # thin entrypoint
internal/cli/cli.go        # CLI parsing + summary output
internal/dedup/engine.go   # dedup engine
cmd/e2e/main.go            # E2E executable scenario
.adl.yaml                  # architecture document
```

## License

This project is licensed under the GNU Affero General Public License v3.0.
See `LICENSE` for the full text.
