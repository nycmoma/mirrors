# mirrors

Purpose-built Debian mirror manager.

The old `mirror.py` port is saved in `backup/main.go`. Current development is
moving toward the final design described in `DESIGN.md` and `PLAN.md`: replace
the subset of aptly needed for mirror workflows without depending on the aptly
binary.

## Usage

Phase 1 provides command parsing, config parsing, validation, and dispatch
scaffolding. Most workflows intentionally return "not implemented yet" until the
underlying packages are built.

```bash
go run . --help
go run . config validate -c /etc/mirrors/ubuntu-xenial.conf
go run . config show -c /etc/mirrors/ubuntu-xenial.conf
go run . create -c /etc/mirrors/ubuntu-xenial.conf
go run . rollback -n ubuntu-xenial -d 2024-12-01
```

Command shape:

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
```

Rules:

- `--URL` remains uppercase by design.
- `-c` always means `--config`.
- `-d` always means `--date`.
- Cleanup only removes data when `--remove` is provided.
