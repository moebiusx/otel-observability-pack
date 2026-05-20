# Repository Workflow Instructions

## Branch Workflow

- Use `develop` as the normal working branch for implementation, documentation, tests, and commits.
- Do not commit directly to `main` unless the user explicitly asks for an emergency direct change.
- Keep local `main` aligned with `origin/main`; treat it as the protected integration target.
- Start new work from the latest `origin/develop`. If `develop` is missing in a fresh clone, create it from `origin/main` before making changes.
- Push completed work to `origin/develop` or to a feature branch based on `develop`, according to the user's request.
- After validation and CI pass, open a pull request from `develop` to `main` for review and merge.

## Validation Before Push or PR

- This is a Go project. Run the repository's standard checks before pushing code changes:
  - `go build ./...`
  - `go test ./...`
  - `go vet ./...`
- For documentation-only changes, a full test run is optional unless the user asks for it or the docs affect generated/tested content (e.g., the spec, schema, or example pack consumed by `packlint`).
- If any validation cannot be run, state that clearly in the final summary.

## Git Hygiene

- Check `git status` before staging, committing, rebasing, or pushing.
- Stage only files related to the current task.
- Never overwrite or revert user changes unless the user explicitly requests it.
- If a push is rejected because the remote moved, fetch first, inspect the branch relationship, and rebase or merge according to the repo workflow.
- Prefer non-interactive git commands.
