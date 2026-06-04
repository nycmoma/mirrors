package cli

// HelpText returns the top-level command help.
func HelpText() string {
	return `Usage:
  mirror config generate -u|--URL <release_url>
  mirror config validate -c|--config <config_file>
  mirror config show [-n|--name <mirror_name> | -c|--config <config_file>]

  mirror create -c|--config <config_file>
  mirror fetch -c|--config <config_file>
  mirror update [-n|--name <mirror_name> | -c|--config <config_file>]
  mirror rollback [-n|--name <mirror_name> | -c|--config <config_file>] [-d|--date YYYY-MM-DD | -i|--id <snapshot_id>]
  mirror daily
  mirror weekly
  mirror monthly
  mirror hide [-n|--name <mirror_name> | -c|--config <config_file>]
  mirror destroy [-n|--name <mirror_name> | -c|--config <config_file>]
  mirror cleanup [-n|--name <mirror_name> | -c|--config <config_file>] [--days <days> | --all]
  mirror list
  mirror info [-n|--name <mirror_name> | -u|--URL <repo_url> | -c|--config <config_file>] [-s|--snapshot <snapshot_id>]
  mirror more-info [-n|--name <mirror_name> | -c|--config <config_file>]

Rules:
  - --URL is intentionally uppercase.
  - -c always means --config.
  - -d always means --date.
  - cleanup only removes snapshots/packages when --days or --all is provided.
`
}
