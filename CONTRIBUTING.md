# Contributing to wp-stage-sync

Thanks for your interest in contributing! This guide covers everything you need to get started.

## Prerequisites

- [Go 1.26+](https://go.dev/dl/)
- A Linux server (or VM) with WordPress sites to test against
- `rsync` and `mysql` client available in PATH

## Getting Started

```bash
git clone git@github.com:AdaDigitalAgency/wp-stage-sync.git
cd wp-stage-sync
go mod download
make build
```

This produces a `wp-stage-sync` binary in the project root.

## Project Structure

```
cmd/wp-stage-sync/   Entry point (CLI flags, unattended mode)
internal/
├── config/          JSON config persistence (~/.config/wp-stage-sync/)
├── db/              MySQL connection (socket fallback, IPv4 forcing)
├── discovery/       Apache/Nginx/LiteSpeed vhost scanning, site pairing
├── export/          Table export engine (HPOS orders, batched INSERTs)
├── guardrail/       Production safety checks (path + DB validation)
├── progress/        Logger interface (CLILogger, NopLogger)
├── selfupdate/      GitHub release update checker
├── release/         Release tool logic: changelog cut, ship plan
├── sync/            File sync (rsync), post-processing (WP-CLI)
├── tui/             Bubbletea TUI (all interactive screens)
├── whatsnew/        Release notes shown in the TUI
├── wpcli/           WP-CLI resolution and PHAR fallback
└── wpconfig/        wp-config.php parser
cmd/release/         Release tool: checks, version bump, changelog, ship
docs/                User documentation, rendered as the website's docs pages
website/             Astro site (landing page, docs, llms.txt)
```

## Development Workflow

### Build and run locally

```bash
make build
./wp-stage-sync
```

The version is embedded at build time via `-ldflags`. Dev builds show `dev` as the version.

### Run on a server

Copy the binary to a Linux server with WordPress installs:

```bash
scp wp-stage-sync user@server:/usr/local/bin/
ssh user@server wp-stage-sync
```

### Code style

- `go vet` and `go build` must pass with no errors.
- Follow standard Go conventions. No linter is enforced, but keep it clean.
- No CGO — the binary must be statically linkable for Linux deploys.

## Making Changes

1. **Fork and branch.** Create a feature branch from `main`.
2. **Keep commits focused.** One logical change per commit.
3. **Test on a real server.** This tool touches live databases and files — there's no substitute for testing against actual WordPress installs.
4. **Update the changelog.** Add your changes to the `[Unreleased]` section of [CHANGELOG.md](CHANGELOG.md) following [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) conventions.
5. **Update the docs.** If behaviour changes, change the page in `docs/` that describes it. The same file is the website page.
6. **Update What's new if users will notice.** Add one to three short lines under `"unreleased"` in [internal/whatsnew/entries.go](internal/whatsnew/entries.go). This is for the person using the TUI, not a copy of the changelog. Most patches need none.

## Releasing

Maintainers release from a clean `main`:

```bash
make release-minor   # or release-patch, release-major
```

The tool does these steps, in order:

1. Refuses a dirty tree, a branch other than `main`, or an empty `[Unreleased]`.
2. Runs gofmt, vet, tests and build.
3. Bumps `.version`, dates the changelog section and renames the What's new `"unreleased"` key.
4. Runs the same checks again, and restores the files if they fail.
5. Commits only those three files as `Release vX.Y.Z`. It never squashes.
6. Asks once, then pushes `main`, waits for CI, tags, pushes the tag and waits for the release workflow.

Run `make ship` to resume after an interruption. It reads the real state and does only the remaining steps. Use `go run ./cmd/release minor --no-ship` to stop at the commit, and `go run ./cmd/release ship --dry-run` to print the steps.

After shipping, the tool asks whether to deploy the website. Pass `--website` to skip the question. To deploy it alone:

```bash
make deploy-website        # dry run: lists the files that differ
make deploy-website GO=1   # publishes them and checks the live page
```

The website is an Astro project in `website/`. Its docs pages are built from `docs/*.md`, read in place, so every doc change is also a site change. To work on it:

```bash
cd website
npm ci
npm run dev      # http://localhost:4321/wp-stage-sync/
npm run build    # astro check, then the static site in website/dist/
```

The website deploy needs `.env` (see `.env.example`) and Node.js 24. It refuses uncommitted changes in `website/` or `docs/`, runs `npm ci` and `npm run build`, uploads `website/dist/`, never uses `--delete`, and writes only to the path pinned in `internal/release/website.go`. A website failure does not affect a shipped release. CI builds the site on every push, so a broken doc fails CI.

Test on a real server before you release. CI does not cover a real WordPress install.

## Pull Requests

- Open a PR against `main`.
- Describe what you changed and why.
- Include the server environment you tested on (distro, WordPress version, WooCommerce version if relevant).

## Reporting Bugs

Open an issue with:

- The version (`wp-stage-sync --version`)
- Server OS and MySQL version
- WordPress and WooCommerce versions (if applicable)
- The error message or unexpected behavior
- Steps to reproduce

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).
