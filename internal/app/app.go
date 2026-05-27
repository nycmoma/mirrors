package app

import (
	"fmt"
	"strings"

	"mirrors/internal/cli"
	"mirrors/internal/config"
	"mirrors/internal/mirror"
	"mirrors/internal/state"
)

// Run is the top-level application entrypoint used by main.
func Run(args []string) error {
	cmd, err := cli.Parse(args)
	if err != nil {
		return err
	}

	if cmd.Help {
		fmt.Print(cli.HelpText())
		return nil
	}

	return dispatch(cmd)
}

func dispatch(cmd cli.Command) error {
	switch cmd.Name {
	case "config":
		return runConfig(cmd)
	case "create", "fetch", "update":
		return runConfigDrivenMirrorCommand(cmd)
	case "rollback", "daily", "weekly", "monthly", "hide", "destroy", "cleanup", "info", "more-info":
		return runMirrorCommand(cmd)
	case "list":
		return runList(cmd)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", cmd.Name, cli.HelpText())
	}
}

func runConfig(cmd cli.Command) error {
	switch cmd.Subcommand {
	case "generate":
		if cmd.URL == "" {
			return fmt.Errorf("missing URL. Use: mirror config generate --URL <repo_url>")
		}
		return notImplemented(cmd.FullName())
	case "validate":
		if cmd.ConfigPath == "" {
			return fmt.Errorf("missing config file. Use: mirror config validate -c <config_file>")
		}
		cfg, err := config.Load(cmd.ConfigPath)
		if err != nil {
			return err
		}
		if err := config.Validate(cfg); err != nil {
			return err
		}
		fmt.Printf("Config %q is valid for mirror %q\n", cmd.ConfigPath, cfg.Name)
		return nil
	case "show":
		if cmd.ConfigPath != "" && cmd.NameRef != "" {
			return fmt.Errorf("ambiguous config identity: provide either --config or --name, not both")
		}
		if cmd.ConfigPath == "" && cmd.NameRef == "" {
			return fmt.Errorf("missing config or name. Use: mirror config show -c <config_file> or mirror config show -n <mirror_name>")
		}
		if cmd.ConfigPath == "" {
			cfg, err := state.LoadMirrorConfig(config.DBPath(cmd.NameRef))
			if err != nil {
				return err
			}
			fmt.Print(cfg.String())
			return nil
		}
		cfg, err := config.Load(cmd.ConfigPath)
		if err != nil {
			return err
		}
		fmt.Print(cfg.String())
		return nil
	default:
		return fmt.Errorf("unknown config command %q. Valid config commands: generate, validate, show", cmd.Subcommand)
	}
}

func runConfigDrivenMirrorCommand(cmd cli.Command) error {
	if cmd.ConfigPath == "" {
		return fmt.Errorf("missing config file. Use: mirror %s -c <config_file>", cmd.Name)
	}

	cfg, err := config.Load(cmd.ConfigPath)
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}

	fmt.Printf("%s would operate on mirror %q\n", title(cmd.Name), cfg.Name)
	fmt.Printf("DB path: %s\n", config.DBPath(cfg.Name))
	fmt.Printf("Mirror components: %s\n", strings.Join(mirror.ComponentMirrorNames(cfg.Name, cfg.Dists, cfg.Releases, cfg.Components), ", "))
	return notImplemented(cmd.Name)
}

func runMirrorCommand(cmd cli.Command) error {
	if cmd.ConfigPath != "" && cmd.NameRef != "" {
		return fmt.Errorf("ambiguous mirror identity: provide either --config or --name, not both")
	}
	if cmd.ConfigPath == "" && cmd.NameRef == "" {
		return fmt.Errorf("missing mirror identity. Use --config <config_file> or --name <mirror_name>")
	}
	if cmd.Name == "cleanup" && cmd.Remove {
		return notImplemented("cleanup --remove")
	}
	return notImplemented(cmd.Name)
}

func runList(cmd cli.Command) error {
	if cmd.Subcommand != "" {
		return fmt.Errorf("list does not accept subcommands")
	}
	return notImplemented("list")
}

func notImplemented(name string) error {
	target, ok := implementationTargets[name]
	if !ok {
		target = implementationTarget{Phase: 11, Name: "App Workflows"}
	}

	return fmt.Errorf("action %q will be implemented in Phase %d: %s.", name, target.Phase, target.Name)
}

type implementationTarget struct {
	Phase int
	Name  string
}

var implementationTargets = map[string]implementationTarget{
	"config generate":  {Phase: 11, Name: "App Workflows"},
	"create":           {Phase: 7, Name: "Mirror Service"},
	"fetch":            {Phase: 7, Name: "Mirror Service"},
	"update":           {Phase: 7, Name: "Mirror Service"},
	"rollback":         {Phase: 8, Name: "Merged Snapshots"},
	"daily":            {Phase: 11, Name: "App Workflows"},
	"weekly":           {Phase: 11, Name: "App Workflows"},
	"monthly":          {Phase: 11, Name: "App Workflows"},
	"hide":             {Phase: 9, Name: "Publish Service"},
	"destroy":          {Phase: 7, Name: "Mirror Service"},
	"cleanup":          {Phase: 11, Name: "App Workflows"},
	"cleanup --remove": {Phase: 11, Name: "App Workflows"},
	"info":             {Phase: 7, Name: "Mirror Service"},
	"more-info":        {Phase: 11, Name: "App Workflows"},
	"list":             {Phase: 7, Name: "Mirror Service"},
}

func title(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
