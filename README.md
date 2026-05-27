# mirrors

Purpose-built Debian mirror manager for internal automation pipelines.

The goal is to manage the repository workflow needed for internal Debian
mirrors: fetching metadata, downloading packages, tracking snapshots,
publishing repository files, and signing releases.

## Current Status

Completed through:

```text
Phase 4: Download Repository Metadata and Packages
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

Next target:

```text
Phase 5: Package Pool
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

Rules:

- `--URL` remains uppercase by design.
- `-c` always means `--config`.
- `-d` always means `--date`.
- Cleanup only removes data when `--remove` is provided.

## Config Format

Configs use an INI-style `[mirror]` section:

```ini
[mirror]
name = asterisk16-bionic
url = http://ppa.launchpad.net/jan-hoffmann/asterisk16/ubuntu/
dist = bionic
release = default
origin = default
label = default
arch = amd64, arm64, i386
components = main
path = asterisk16-bionic
merge = yes
server = http://my-mirrors.com/
```

Important fields:

- `name`: mirror name or prefix.
- `url`: upstream apt repository URL.
- `dist`: distribution or suite, such as `focal` or `jammy`.
- `release`: release pocket, usually `default`.
- `origin` and `label`: values from Release/InRelease, or `default`.
- `arch`: comma-separated architectures.
- `components`: comma-separated components.
- `path`: local/published path name.
- `merge`: optional snapshot merge setting: `no`, `0`, `yes`, or a positive number.
- `server`: optional published mirror URL for generated sources output.

## Build And Test

The project targets Go 1.18.

```bash
gofmt -w main.go internal
go test ./...
go build -buildvcs=false .
```

Use `-buildvcs=false` when building outside a clean VCS context or when Go
cannot read repository VCS metadata.

## Local State Layout

The app state layout is:

```text
~/.mirrors/
  db/
    <mirror_name>.sqlite
  packages/
```

Each mirror/config gets a separate SQLite database:

```text
~/.mirrors/db/<mirror_name>.sqlite
```

Package files will be stored under `~/.mirrors/packages/` using the planned
package pool structure.

## Merge Snapshot Rule

Regular snapshots must represent the exact upstream repository state at update
time.

Regular snapshot name:

```text
<mirror>-<dist[-release]>-<component>_<YYYY-MM-DD>
```

If merge is disabled with `merge = no` or `merge = 0`, publish the regular
snapshot directly.

If merge is enabled, create the regular upstream snapshot first, then create a
separate merged snapshot:

```text
MERGED-<mirror>-<dist[-release]>-<component>_<YYYY-MM-DD>
```

Merge depth:

- `merge = 1`: latest regular snapshot plus 1 previous regular snapshot.
- `merge = 2`: latest regular snapshot plus 2 previous regular snapshots.
- `merge = N`: latest regular snapshot plus N previous regular snapshots.
- `merge = yes`: latest regular snapshot plus all previous regular snapshots.

When merge is enabled, publish the `MERGED-*` snapshot, not the regular
snapshot.
