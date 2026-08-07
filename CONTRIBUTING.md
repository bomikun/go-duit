# Contributing to go-duit

Thanks for your interest! Contributions are welcome.

## Ground rules

- **Money safety first.** Every operation that drops precision must do so
  at an explicit, documented point with an explicit scale. No API may
  introduce silent rounding, and nothing may panic on data that can
  arrive from a database or the network.
- **The public API speaks `Money` only.** The backing decimal library is
  an implementation detail; do not add `decimal.Decimal` to any exported
  signature beyond the existing `NewFromDecimal`/`Decimal` escape hatch.
- **Backward compatibility.** This module follows semver. Breaking
  changes need a major-version discussion first — open an issue.

## Development

```
go test -race ./...   # unit + seed-corpus fuzz tests
go vet ./...
gofmt -l .            # must print nothing
```

Linting matches CI (`.golangci.yml`):

```
golangci-lint run ./...
```

Longer fuzzing runs are appreciated for changes to parsing or allocation:

```
go test -run xxx -fuzz FuzzAllocate -fuzztime 60s .
```

## Pull requests

- Branch from `main`; PRs must pass all required checks
  (`test (1.21.x)`, `test (stable)`, `lint`, `govulncheck`).
- Add tests for any behavior change; keep the exact-sum invariants of
  `Allocate`/`AllocateRatios` covered.
- Update `CHANGELOG.md` under an `Unreleased`/next-version heading.
- Keep the minimum Go version (`go 1.21` in go.mod) unless there is a
  strong, discussed reason to raise it.

## Releases (maintainers)

Releases are tag-driven: push a `vX.Y.Z` tag on `main` and the Release
workflow verifies it and publishes the GitHub Release automatically.
