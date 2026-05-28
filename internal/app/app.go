package app

import (
	"context"
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

	service, err := mirror.NewService()
	if err != nil {
		return err
	}

	switch cmd.Name {
	case "create":
		result, err := service.Create(context.Background(), cfg)
		if err != nil {
			return err
		}
		printFetchResult("Create", result)
		return nil
	case "fetch":
		result, err := service.Fetch(context.Background(), cfg)
		if err != nil {
			return err
		}
		printFetchResult("Fetch", result)
		return nil
	case "update":
		fmt.Printf("%s would operate on mirror %q\n", title(cmd.Name), cfg.Name)
		fmt.Printf("DB path: %s\n", config.DBPath(cfg.Name))
		fmt.Printf("Mirror components: %s\n", strings.Join(mirror.ComponentMirrorNames(cfg.Name, cfg.Dists, cfg.Releases, cfg.Components), ", "))
		return notImplemented(cmd.Name)
	default:
		return notImplemented(cmd.Name)
	}
}

func runMirrorCommand(cmd cli.Command) error {
	if cmd.ConfigPath != "" && cmd.NameRef != "" {
		return fmt.Errorf("ambiguous mirror identity: provide either --config or --name, not both")
	}
	if cmd.ConfigPath == "" && cmd.NameRef == "" {
		return fmt.Errorf("missing mirror identity. Use --config <config_file> or --name <mirror_name>")
	}
	name, err := mirrorNameFromCommand(cmd)
	if err != nil {
		return err
	}
	service, err := mirror.NewService()
	if err != nil {
		return err
	}
	switch cmd.Name {
	case "info":
		summary, err := service.Info(name)
		if err != nil {
			return err
		}
		printSummary(summary)
		return nil
	case "destroy":
		if err := service.Destroy(name); err != nil {
			return err
		}
		fmt.Printf("Destroyed mirror %q\n", name)
		return nil
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
	service, err := mirror.NewService()
	if err != nil {
		return err
	}
	summaries, err := service.List()
	if err != nil {
		return err
	}
	if len(summaries) == 0 {
		fmt.Println("No mirrors found")
		return nil
	}
	for _, summary := range summaries {
		printSummary(summary)
	}
	return nil
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
	"update":           {Phase: 8, Name: "Merged Snapshots"},
	"rollback":         {Phase: 8, Name: "Merged Snapshots"},
	"daily":            {Phase: 11, Name: "App Workflows"},
	"weekly":           {Phase: 11, Name: "App Workflows"},
	"monthly":          {Phase: 11, Name: "App Workflows"},
	"hide":             {Phase: 9, Name: "Publish Service"},
	"cleanup":          {Phase: 11, Name: "App Workflows"},
	"cleanup --remove": {Phase: 11, Name: "App Workflows"},
	"more-info":        {Phase: 11, Name: "App Workflows"},
}

func title(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func mirrorNameFromCommand(cmd cli.Command) (string, error) {
	if cmd.NameRef != "" {
		return cmd.NameRef, nil
	}
	cfg, err := config.Load(cmd.ConfigPath)
	if err != nil {
		return "", err
	}
	if err := config.Validate(cfg); err != nil {
		return "", err
	}
	return cfg.Name, nil
}

func printFetchResult(action string, result mirror.FetchResult) {
	fmt.Printf("%s completed for mirror %q\n", action, result.MirrorName)
	fmt.Printf("DB path: %s\n", result.DBPath)
	fmt.Printf("Indexes: %d\n", result.IndexCount)
	fmt.Printf("Packages: %d\n", result.PackageCount)
	fmt.Printf("Downloaded: %d\n", result.DownloadedCount)
	fmt.Printf("Reused: %d\n", result.ReusedCount)
	fmt.Printf("Changes: +%d -%d\n", result.AddedPackageCount, result.RemovedPackageCount)
}

func printSummary(summary mirror.Summary) {
	fmt.Printf("Mirror: %s\n", summary.Config.Name)
	fmt.Printf("DB path: %s\n", summary.DBPath)
	fmt.Printf("URL: %s\n", summary.Config.URL)
	fmt.Printf("Suites: %s\n", strings.Join(suites(summary.Config), ", "))
	fmt.Printf("Components: %s\n", strings.Join(summary.Config.Components, ", "))
	fmt.Printf("Architectures: %s\n", strings.Join(summary.Config.Arch, ", "))
	fmt.Printf("Packages: %d current, %d known\n", summary.Stats.MirrorPackageCount, summary.Stats.KnownPackageCount)
	fmt.Printf("Snapshots: %d\n", summary.Stats.SnapshotCount)
	if summary.Stats.LastUpdate != nil {
		fmt.Printf("Last update: %s %s\n", summary.Stats.LastUpdate.Status, summary.Stats.LastUpdate.FinishedAt.Format("2006-01-02T15:04:05Z07:00"))
	} else {
		fmt.Println("Last update: never")
	}
}

func suites(cfg config.Mirror) []string {
	var values []string
	for _, dist := range cfg.Dists {
		for _, release := range cfg.Releases {
			values = append(values, mirror.SuiteName(dist, release))
		}
	}
	return values
}
