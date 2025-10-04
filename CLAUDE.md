# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

helm-diff is a Helm v3 plugin that provides diff functionality for comparing Helm charts and releases. It shows what changes would occur during helm upgrade, rollback, or between different releases/revisions.

## Development Commands

### Build and Test
```bash
# Bootstrap (run once or when dependencies change)
make bootstrap              # Downloads dependencies and installs staticcheck (~50s first time)

# Build
make build                  # Runs linting and compiles binary (~9s)

# Test
make test                   # Run tests with coverage (~12s)
go test -v ./...           # Run tests without coverage

# Code Quality
make format                 # Auto-format code with gofmt
make lint                   # Run gofmt, go vet, and staticcheck

# Install as Plugin
make install/helm3          # Install to Helm plugins directory
```

### Direct Binary Testing
```bash
# Build binary directly
go build -o bin/diff -ldflags="-X github.com/databus23/helm-diff/v3/cmd.Version=dev"

# Test without plugin installation
HELM_NAMESPACE=default HELM_BIN=helm ./bin/diff upgrade --install --dry-run my-release ./chart-path
```

## Commands Overview

**upgrade** - Compare deployed release with chart (most common)
**local** - Compare two local chart directories
**git** - Compare charts at different git references
**release** - Compare two deployed releases
**revision** - Compare two revisions of same release
**rollback** - Preview what a rollback would do

## Architecture

### Core Packages

**cmd/** - Command-line interface using cobra
- `root.go` - Root command setup, color handling, and output format configuration
- `upgrade.go` - Main diff upgrade command that compares deployed release with new chart
- `local.go` - Compares two local chart directories; exports `Local` type for reuse
- `git.go` - Compares charts at different git refs; reuses `Local.Run()` internally
- `release.go` - Compares manifests between two releases
- `revision.go` - Compares manifests between two revisions of a release
- `rollback.go` - Shows what a helm rollback would change
- `helm3.go` - Helm v3 integration (runs helm template/upgrade dry-run)

**manifest/** - Kubernetes manifest parsing and handling
- `parse.go` - Parses YAML manifests into MappingResult structures, handles Lists, hooks, and metadata extraction
- `generate.go` - Three-way merge support for comparing old release, new release, and current cluster state
- `util.go` - Utilities for cleaning status/metadata from manifests

**diff/** - Core diffing logic and output
- `diff.go` - Main diffing engine with rename detection, secret handling (redaction/decoding), and change detection
- `report.go` - Report generation and formatting (supports diff, simple, template, dyff output formats)
- `constant.go` - Constants for Helm hook types

### Key Concepts

**Two Modes of Operation:**
1. **With cluster access** (default): Fetches deployed release from cluster, supports `--validate`, `--dry-run=server`
2. **Without cluster access** (`--dry-run=client`): Uses helm template only, no cluster access

**Three-Way Merge** (`--three-way-merge`): Compares old release manifest, new release manifest, and current cluster state. Required for `--take-ownership` which allows adopting resources from other releases.

**Secret Handling:**
- Default: Redacts secret data showing only byte counts
- `--show-secrets`: Shows raw base64 data
- `--show-secrets-decoded`: Decodes and shows plain text

**Rename Detection** (`--find-renames`): Detects when resources are renamed by comparing content similarity, reducing false add/remove pairs.

**Manifest Normalization** (`--normalize-manifests`): Normalizes YAML structure to ignore style differences.

## Important Environment Variables

- `HELM_DIFF_USE_UPGRADE_DRY_RUN=true` - Use `helm upgrade --dry-run` instead of `helm template`
- `HELM_DIFF_THREE_WAY_MERGE=true` - Enable three-way merge
- `HELM_DIFF_NORMALIZE_MANIFESTS=true` - Enable manifest normalization
- `HELM_DIFF_OUTPUT_CONTEXT=n` - Set context lines
- `HELM_DIFF_COLOR=[true|false]` - Control color output
- `HELM_DIFF_IGNORE_UNKNOWN_FLAGS=true` - Ignore unknown flags (for wrapper scripts)
- `HELM_BIN` - Path to helm binary (for direct testing)
- `HELM_NAMESPACE` - Default namespace

## Testing Approach

Tests use a fake helm binary approach for isolation (see testdata/). The test suite includes:
- Unit tests for all major components
- Comprehensive manifest parsing tests
- Diff output format tests
- Secret handling tests

Coverage is tracked per-function in cover.out.

## Common Patterns

**Adding a new command:**
1. Create command function in cmd/ (see `local.go` or `git.go` as examples)
2. Add to root.go in the AddCommand() call
3. Follow existing flag patterns from other commands
4. Use `diff.Options` for common diff options via AddDiffOptions()
5. For chart rendering flags, use `addChartRenderFlags()` helper

**Reusing local diff logic:**
The `Local` type and its `Run()` method are exported for reuse by other commands like `git`. Create a `Local` instance, set `Chart1` and `Chart2` fields, then call `Run()`.

**Git command implementation:**
- Uses `git archive` to extract refs to temp directories without modifying working tree
- For working tree comparison, uses cross-platform `copyDir()` function
- Automatically runs `helm dependency build` for each extracted ref to handle chart dependencies
- Detects primary branch via `git symbolic-ref refs/remotes/{remote}/HEAD`
- Chart path is always relative to repository root for consistency
- Environment variables `GIT_BIN` and `HELM_BIN` can override binary locations

**Manifest processing flow:**
1. Render charts (helm template or helm upgrade --dry-run)
2. Parse YAML into MappingResult map keyed by "namespace, name, kind (apiVersion)"
3. Filter hooks/tests based on flags
4. Generate diff comparing old vs new manifests
5. Format output based on --output flag

**Output formats:**
- `diff` (default): Traditional unified diff with +/- markers
- `simple`: Simplified diff without context
- `template`: Custom template via HELM_DIFF_TPL env var
- `dyff`: Use dyff library for advanced YAML-aware diff

## Plugin Installation

The plugin installs via `install-binary.sh` which:
1. Detects OS/arch
2. Downloads appropriate binary from GitHub releases
3. Installs to Helm plugins directory

Multi-platform support: Linux (amd64/arm64/armv6/armv7/ppc64le/s390x), macOS (amd64/arm64), Windows (amd64), FreeBSD (amd64)
