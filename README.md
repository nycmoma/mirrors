# mirrors

Purpose-built Debian mirror manager for internal automation pipelines.

The goal is to manage the repository workflow needed for internal Debian
mirrors: fetching metadata, downloading packages, tracking snapshots,
publishing repository files, and signing releases.

## Current Status

Completed through:

```text
Phase 16: Mirror Update Policy
```

Phase 17 workflow consistency and state ordering work is planned but not
started.

Implemented packages and behavior:

- CLI parsing and app dispatch.
- INI-style `[mirror]` config parsing, validation, and normalized rendering.
- Debian metadata parsing in `internal/debmeta`:
  - stanzas
  - `Release`
  - `InRelease` clear-signed payload extraction
  - `Packages`, `Packages.gz`, `Packages.xz`
  - package checksums
  - Debian version comparison
- Download support in `internal/download`:
  - configurable HTTP timeout
  - retry handling
  - retry diagnostics when file logging is configured
  - metadata fetch
  - package/file download
  - package download byte-progress callbacks
  - size and checksum verification
  - `HEAD` length lookup
  - testable downloader interface
- Package pool support in `internal/pool`:
  - checksum-based storage layout under the configured package pool root
  - package import with size and checksum verification
  - duplicate package detection
  - existing package verification
  - disk usage reporting
  - guarded package removal when reference data is provided
- SQLite state support in `internal/state`:
  - automatic per-mirror DB creation and schema migrations
  - mirror config persistence
  - package metadata upsert
  - upstream Release origin/label persistence
  - upstream package stanza field persistence
  - current mirror package membership replacement
  - dated snapshot records and snapshot package membership
  - published state switching
  - update history records
  - cleanup reference queries
  - transaction helpers for multi-table workflow updates
- Mirror service support in `internal/mirror`:
  - upstream Release and Packages index URL resolution
  - Release validation for configured origin, label, components, and architectures
  - download planning before package downloads
  - disk-space checking before package downloads
  - bounded concurrent package downloads using `download_threads`
  - package download progress events
  - package index fetching and parsing
  - missing package downloads into the local package pool
  - idempotent package reuse when files already exist in the pool
  - current mirror package membership updates
  - mirror list, info, create, fetch, and destroy operations
- Snapshot support in `internal/snapshot`:
  - local-date regular snapshot creation
  - same-day snapshot regeneration under the same name
  - older dated snapshot preservation
  - `MERGED-*` snapshot creation when merge is enabled
  - merge-depth handling for numeric merge settings and merge-all behavior
  - checksum-conflict warnings with newest package selection
  - snapshot listing and `info --snapshot` lookup
  - rollback snapshot selection
- Unsigned publish support in `internal/publish`:
  - `Packages` and `Packages.gz` generation from stored upstream stanza data
  - unsigned `Release` metadata generation
  - upstream or explicit origin/label selection
  - configured-root-relative publish paths
  - package hardlinking from the local package pool with copy fallback
  - publish switching for create, update, and rollback
  - hide/unpublish while preserving mirror state, snapshots, and packages
- Signing support in `internal/signing`:
  - default-on `gpg` signing for published repository metadata
  - optional `sign = no` config to publish unsigned output
  - config, environment, and default-key signing resolution
  - passphrase support from config value, passphrase file, or environment
  - `InRelease` and `Release.gpg` generation from the current `Release`
  - stale signature removal before each signing attempt
  - diagnostic logging without passphrase values
- App workflow support in `internal/app` currently includes:
  - `config generate` starter config rendering from a Release/InRelease URL
  - global application config loading and validation
  - optional file-backed diagnostic logging
  - download estimate output for package-consuming workflows
  - interactive package download progress bar with line-based log fallback
  - `daily`, `weekly`, and `monthly` batch update commands for CI/CD or
    manual scheduling
  - cleanup summary reporting for snapshots and unreferenced package pool files
  - cleanup retention with `--days` or `--all`
  - published snapshot preservation during cleanup
  - expanded `more-info` output

## Global Config

The app reads global defaults from:

```text
$XDG_CONFIG_HOME/mirrors.conf
~/.config/mirrors.conf
```

If no global config exists, the app tries to create one with these defaults:

```ini
[app]
data_root = ~/.mirrors/.data
mirrors_root = ~/.mirrors/mirrors
logs_root = ~/.mirrors/.logs
http_timeout = 30s
http_retries = 3
http_retry_delay = 1s
download_threads = 4
log_level = info
log_file =
```

`data_root` stores SQLite DB files under `db/` and package files under
`packages/`. `mirrors_root` is the base directory for relative publish paths.
The app creates missing root directories and requires `data_root`,
`mirrors_root`, and `logs_root` to be directories and writable before startup
continues.

`log_file` is optional. When empty, no diagnostic log file is created. Absolute
log paths are used directly. Relative log paths are resolved from `logs_root`,
so `..` path segments can intentionally place logs relative to that root.
`log_level` accepts `error`, `warn`, `info`, or `debug`.

## Available Actions

Currently implemented user-facing actions:

```text
mirror --help
mirror config generate -u|--URL <release_url>
mirror config validate -c|--config <config_file>
mirror config show -c|--config <config_file>
mirror config show -n|--name <mirror_name>
mirror create -c|--config <config_file>
mirror fetch -c|--config <config_file>
mirror update [-n|--name <mirror_name> | -c|--config <config_file>]
mirror daily
mirror weekly
mirror monthly
mirror rollback [-n|--name <mirror_name> | -c|--config <config_file>] [-d|--date YYYY-MM-DD | -i|--id <snapshot_id>]
mirror hide [-n|--name <mirror_name> | -c|--config <config_file>]
mirror cleanup [-n|--name <mirror_name> | -c|--config <config_file>] [--days <days> | --all]
mirror list
mirror info [-n|--name <mirror_name> | -u|--URL <repo_url> | -c|--config <config_file>] [-s|--snapshot <snapshot_id>]
mirror more-info [-n|--name <mirror_name> | -c|--config <config_file>]
mirror destroy [-n|--name <mirror_name> | -c|--config <config_file>]
```

`config show -n` reads normalized config data from:

```text
~/.mirrors/.data/db/<mirror_name>.sqlite
```

New DB files are created automatically when the state package opens a mirror
database.

Published repository output now includes signed metadata by default:

```text
Packages
Packages.gz
Release
InRelease
Release.gpg
```

Signing can be disabled per mirror with `sign = no`. When signing is enabled,
`gpg_key`, `gpg_home`, `gpg_passphrase`, and `gpg_passphrase_file` can be set
in `[mirror]`; otherwise `GPG_KEY` and `GPG_PASSPHRASE` are used when present,
with `gpg` default key behavior as the final fallback.

Scheduled automation can set per-mirror update intent with:

```text
update = daily|weekly|monthly|never
```

Empty or missing `update` means no schedule policy is configured. `never`
allows mirror creation but blocks `update`, `daily`, `weekly`, and `monthly`.
`daily` and `weekly` use one-day and seven-day currently published snapshot age
gates. `monthly` uses a calendar-month gate, not a fixed 30-day duration.

`mirror daily`, `mirror weekly`, and `mirror monthly` take no mirror identity
flags. They enumerate created DB mirrors, refresh each stored config file into
the DB, skip unpublished or not-due mirrors with terminal and info-log reasons,
and update only mirrors whose refreshed policy matches the command.

## Usage Examples

```bash
mirror --help
mirror config generate --URL http://us.archive.ubuntu.com/ubuntu/dists/bionic/Release
mirror config validate -c ./chrome_stable.conf
mirror config show -c ./chrome_stable.conf
mirror config show -n chrome_stable
mirror fetch -c ./chrome_stable.conf
mirror update -c ./chrome_stable.conf
mirror update -n chrome_stable
mirror daily
mirror rollback -n chrome_stable -d 2026-05-27
mirror hide -n chrome_stable
mirror cleanup -n chrome_stable
mirror cleanup -n chrome_stable --days 30
mirror cleanup -n chrome_stable --all
mirror list
mirror info -n chrome_stable
mirror more-info -n chrome_stable
```
