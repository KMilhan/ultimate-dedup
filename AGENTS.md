# Repository Guidelines

## Project Structure & Module Organization
- `go.mod`: Go module definition and dependencies.
- `main.go`: Main entry point for the application.
- `AGENTS.md`: Operational guidelines for AI agents working on this repository.

## Build, Test, and Development Commands
- Run: `go run .`
- Build: `go build -o ultimate-dedup .`
- Format: `go fmt ./...`
- Lint: `go vet ./...` (and `golangci-lint run` if available).
- **Always run `go fmt ./...` and `go vet ./...` before committing/pushing.**
- **Always run `go test ./...` whenever new logic is added or tests change.**
- GitHub CLI: `gh repo set-default KMilhan/ultimate-dedup`. Common commands: `gh pr create -f`, `gh pr view -w`, `gh pr status`.

## Coding Style & Naming Conventions
- Go: Follow standard Go conventions (Effective Go).
    - Use `gofmt` for formatting.
    - `PascalCase` (Exported) and `camelCase` (unexported) for names.
    - Short, concise variable names where appropriate (e.g., `i`, `ctx`).

## Testing Guidelines
- Use the standard `testing` package.
- Create `*_test.go` files next to the code they test.
- Run `go test -v ./...` to run tests with verbose output.

## Commit & Pull Request Guidelines
- Commit style: colon-based emoji prefix, no category/scope. Format `:<emoji>:` + imperative subject.
    - Examples: `:sparkles: add file hashing`, `:test: add unit tests`, `:wrench: update go.mod`, `:bug: fix panic on empty file`.
- Atomic and immediate: keep each commit a single logical change; commit and push right away.
- Verbose messages: include a clear subject and a short body explaining why and what changed.
- PRs include: summary, linked issues, and confirmation that tests pass.

## Issue-Driven Workflow (Loop)
- Status labels: use `status:ready`, `status:in-progress`, `status:blocked`, `status:review`. Create if missing: `gh label create "status:ready" --color 0366d6`.
- Pick next task: open, non-epic issues labeled `status:ready` (prefer `priority:high`, then `type:task`).
- Start work: mark in progress and leave a short comment.
    - `gh issue edit <n> --add-label "status:in-progress" --remove-label "status:ready"`
    - `gh issue comment <n> -b "Starting implementation."`
- Implement: keep commits atomic; push immediately; reference the issue in the body.
    - `git commit -m ":sparkles: implement feature" -m "why: ...; what: ...; closes: #<n>" && git push`
- Close: after push, close the issue with a link to the commit/PR.
    - `gh issue close <n> -c "Done in <sha/url>"`

## One-Phrase Command: "start work"
- Trigger: When you say "start work" (optionally with an issue number), the agent will autonomously:
    1) Ensure GitHub CLI is scoped and labels exist
        - `gh repo set-default KMilhan/ultimate-dedup`
        - Create missing status labels: `status:ready|in-progress|blocked|review`.
    2) Select the most relevant open, non-epic issue
        - Preference order: `status:ready` > `priority:high` > `type:task` > oldest updated.
        - Fallbacks: if none ready, pick any open `priority:high`; else the oldest open.
        - To target a specific issue: say `start work #<n>`.
    3) Start the issue
        - Apply `status:in-progress`, remove `status:ready`; add a brief comment with intended steps.
    4) Implement and push immediately
        - Make minimal, atomic changes; commit with `:<emoji>:` subject and verbose body including `closes: #<n>`; push to `main`.
    5) Close the issue
        - `gh issue close <n> -c "Completed in <sha/url>"`.
    6) Stop after one issue unless you say "start work continuously" (loops to the next ready issue).

- "preview start": list the candidate issue and planned steps without making changes.
- "pause work": remove `status:in-progress`, re-add `status:ready`, and note why.
- "next": close current if done, then immediately pick the next ready issue.
