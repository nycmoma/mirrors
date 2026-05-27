# mirrors

Purpose-built Debian mirror manager for internal automation pipelines.

The goal is to manage the repository workflow needed for internal Debian
mirrors: fetching metadata, downloading packages, tracking snapshots,
publishing repository files, and signing releases.

## Current Status

Completed through:

```text
Phase 6: State Store (Snapshots, Package Membership, Published State)
```

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
  - metadata fetch
  - package/file download
  - size and checksum verification
  - `HEAD` length lookup
  - testable downloader interface
- Package pool support in `internal/pool`:
  - checksum-based storage layout under `~/.mirrors/packages/`
  - package import with size and checksum verification
  - duplicate package detection
  - existing package verification
  - disk usage reporting
  - guarded package removal when reference data is provided
- SQLite state support in `internal/state`:
  - automatic per-mirror DB creation and schema migrations
  - mirror config persistence
  - package metadata upsert
  - current mirror package membership replacement
  - immutable snapshot records and snapshot package membership
  - published state switching
  - update history records
  - cleanup reference queries
  - transaction helpers for multi-table workflow updates

Next target:

```text
Phase 7: Mirror Service
```

## Available Actions

Currently implemented user-facing actions:

```text
mirror --help
mirror config validate -c|--config <config_file>
mirror config show -c|--config <config_file>
mirror config show -n|--name <mirror_name>
```

`config show -n` reads normalized config data from:

```text
~/.mirrors/db/<mirror_name>.sqlite
```

New DB files are created automatically when the state package opens a mirror
database.

End-to-end mirror workflows such as `create`, `fetch`, `update`, `rollback`,
`hide`, and `cleanup` are not wired yet. They report the planned phase:

```text
ERROR: action "create" will be implemented in Phase 7: Mirror Service.
```

## Usage Examples

```bash
go run . --help
go run . config validate -c ./chrome_stable.conf
go run . config show -c ./chrome_stable.conf
go run . config show -n chrome_stable
```

## Planned Command Shape

```text
mirror config generate -u|--URL <repo_url>
mirror config validate -c|--config <config_file>
mirror config show [-n|--name <mirror_name> | -c|--config <config_file>]

mirror create -c|--config <config_file>
mirror fetch -c|--config <config_file>
mirror update -c|--config <config_file>
mirror rollback [-n|--name <mirror_name> | -c|--config <config_file>] [-d|--date YYYY-MM-DD | -i|--id <snapshot_id>]
mirror daily [-n|--name <mirror_name> | -c|--config <config_file>]
mirror weekly [-n|--name <mirror_name> | -c|--config <config_file>]
mirror monthly [-n|--name <mirror_name> | -c|--config <config_file>]
mirror hide [-n|--name <mirror_name> | -c|--config <config_file>]
mirror destroy [-n|--name <mirror_name> | -c|--config <config_file>]
mirror cleanup [-n|--name <mirror_name> | -c|--config <config_file>] [-d|--date YYYY-MM-DD] [--remove]
mirror list
mirror info [-n|--name <mirror_name> | -c|--config <config_file>] [-s|--snapshot <snapshot_id>]
mirror more-info ...
```
