package app

import (
	"context"
	"fmt"
	"strings"

	"mirrors/internal/cli"
	"mirrors/internal/config"
	"mirrors/internal/mirror"
	"mirrors/internal/snapshot"
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
		fetchResult, err := service.Fetch(context.Background(), cfg)
		if err != nil {
			return err
		}
		snapshotService, err := snapshot.NewService()
		if err != nil {
			return err
		}
		updateResult, err := snapshotService.CreateCurrent(cfg)
		if err != nil {
			return err
		}
		printFetchResult("Update fetch", fetchResult)
		printUpdateResult(updateResult)
		return nil
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
	case "rollback":
		snapshotService, err := snapshot.NewService()
		if err != nil {
			return err
		}
		result, err := snapshotService.Rollback(name, cmd.Date, cmd.ID)
		if err != nil {
			return err
		}
		printRollbackResult(result)
		return nil
	case "info":
		summary, err := service.Info(name)
		if err != nil {
			return err
		}
		printSummary(summary)
		snapshotService, err := snapshot.NewService()
		if err != nil {
			return err
		}
		if cmd.Snapshot != "" {
			snapshotSummary, err := snapshotService.Snapshot(name, cmd.Snapshot)
			if err != nil {
				return err
			}
			printSnapshotSummary(snapshotSummary)
			return nil
		}
		snapshots, err := snapshotService.List(name)
		if err != nil {
			return err
		}
		printSnapshotList(snapshots)
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
	if summary.Stats.Published != nil {
		fmt.Printf("Selected snapshot: %s\n", summary.Stats.Published.SnapshotName)
	} else {
		fmt.Println("Selected snapshot: none")
	}
	if summary.Stats.LastUpdate != nil {
		fmt.Printf("Last update: %s %s\n", summary.Stats.LastUpdate.Status, summary.Stats.LastUpdate.FinishedAt.Format("2006-01-02T15:04:05Z07:00"))
	} else {
		fmt.Println("Last update: never")
	}
}

func printUpdateResult(result snapshot.UpdateResult) {
	fmt.Printf("Snapshot date: %s\n", result.Date)
	for _, item := range result.Snapshots {
		action := "created"
		if item.Regenerated {
			action = "regenerated"
		}
		fmt.Printf("Snapshot %s: %s (%s, %d packages)\n", action, item.Name, item.Kind, item.PackageCount)
	}
	for _, warning := range result.Warnings {
		fmt.Printf("WARNING: %s\n", warning)
	}
	fmt.Printf("Selected snapshot: %s\n", result.SelectedSnapshot)
	fmt.Println("Published repository files are not generated in Phase 8.")
}

func printRollbackResult(result snapshot.RollbackResult) {
	fmt.Printf("Rollback selected snapshot for mirror %q\n", result.MirrorName)
	fmt.Printf("DB path: %s\n", result.DBPath)
	fmt.Printf("Selected snapshot: %s\n", result.SelectedSnapshot)
	if len(result.ResolvedSnapshots) > 1 {
		fmt.Printf("Resolved snapshot group: %s\n", strings.Join(result.ResolvedSnapshots, ", "))
	}
	fmt.Println("Published repository files are not generated in Phase 8.")
}

func printSnapshotList(snapshots []snapshot.Summary) {
	if len(snapshots) == 0 {
		fmt.Println("Snapshot list: none")
		return
	}
	fmt.Println("Snapshot list:")
	for _, item := range snapshots {
		fmt.Printf("- %s (%s, %d packages, created %s)\n",
			item.Record.Name,
			item.Record.Kind,
			item.PackageCount,
			item.Record.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		)
	}
}

func printSnapshotSummary(item snapshot.Summary) {
	fmt.Printf("Snapshot: %s\n", item.Record.Name)
	fmt.Printf("Kind: %s\n", item.Record.Kind)
	fmt.Printf("Created: %s\n", item.Record.CreatedAt.Format("2006-01-02T15:04:05Z07:00"))
	fmt.Printf("Packages: %d\n", item.PackageCount)
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
