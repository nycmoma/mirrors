package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"mirrors/internal/appconfig"
	"mirrors/internal/cli"
	"mirrors/internal/config"
	"mirrors/internal/download"
	"mirrors/internal/logging"
	"mirrors/internal/mirror"
	"mirrors/internal/pool"
	"mirrors/internal/publish"
	"mirrors/internal/signing"
	"mirrors/internal/snapshot"
	"mirrors/internal/state"
)

var generateConfig = func(ctx context.Context, rawURL string, downloader download.Downloader) (config.Mirror, error) {
	return config.GenerateWithDownloader(ctx, rawURL, downloader)
}
var validateConfig = validateConfigWorkflowWithLogger
var loadAppConfig = appconfig.Load
var runUpdateWorkflow = runPublishUpdateWithLogger
var refreshMirrorConfigForPeriodic = refreshMirrorConfigByName
var loadPublishedForPeriodic = loadPublishedWithConfig

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

	appCfg, err := loadAppConfig()
	if err != nil {
		return err
	}
	logger, err := appCfg.NewLogger()
	if err != nil {
		return err
	}
	defer func() {
		_ = logger.Close()
	}()
	logger.Infof("command start name=%q subcommand=%q config=%q name=%q url_set=%t", cmd.Name, cmd.Subcommand, cmd.ConfigPath, cmd.NameRef, cmd.URL != "")
	logger.Debugf("app config path=%q data_root=%q mirrors_root=%q logs_root=%q log_file=%q", appCfg.Path, appCfg.DataRoot, appCfg.MirrorsRoot, appCfg.LogsRoot, appCfg.LogFile)
	err = dispatchWithLogger(cmd, appCfg, logger)
	if err != nil {
		logger.Errorf("command failed name=%q error=%v", cmd.Name, err)
		return err
	}
	logger.Infof("command complete name=%q", cmd.Name)
	return nil
}

func dispatch(cmd cli.Command, appCfg appconfig.Config) error {
	return dispatchWithLogger(cmd, appCfg, logging.Nop())
}

func dispatchWithLogger(cmd cli.Command, appCfg appconfig.Config, logger logging.Logger) error {
	switch cmd.Name {
	case "config":
		return runConfigWithConfigAndLogger(cmd, appCfg, logger)
	case "create", "fetch":
		return runConfigDrivenMirrorCommandWithLogger(cmd, appCfg, logger)
	case "update":
		return runUpdateCommandWithLogger(cmd, appCfg, logger)
	case "rollback", "daily", "weekly", "monthly", "hide", "destroy", "cleanup", "info", "more-info":
		return runMirrorCommandWithConfigAndLogger(cmd, appCfg, logger)
	case "list":
		return runListWithConfigAndLogger(cmd, appCfg, logger)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", cmd.Name, cli.HelpText())
	}
}

func runConfig(cmd cli.Command) error {
	appCfg, err := loadAppConfig()
	if err != nil {
		return err
	}
	return runConfigWithConfig(cmd, appCfg)
}

func runConfigWithConfig(cmd cli.Command, appCfg appconfig.Config) error {
	return runConfigWithConfigAndLogger(cmd, appCfg, logging.Nop())
}

func runConfigWithConfigAndLogger(cmd cli.Command, appCfg appconfig.Config, logger logging.Logger) error {
	switch cmd.Subcommand {
	case "generate":
		if cmd.URL == "" {
			return fmt.Errorf("missing URL. Use: mirror config generate --URL <release_url>")
		}
		logger.Debugf("config generate url=%q", cmd.URL)
		cfg, err := generateConfig(context.Background(), cmd.URL, appCfg.NewDownloaderWithLogger(logger))
		if err != nil {
			return err
		}
		fmt.Print(cfg.String())
		return nil
	case "validate":
		if cmd.ConfigPath == "" {
			return fmt.Errorf("missing config file. Use: mirror config validate -c <config_file>")
		}
		cfg, err := config.Load(cmd.ConfigPath)
		if err != nil {
			return err
		}
		logger.Debugf("config validate path=%q mirror=%q", cmd.ConfigPath, cfg.Name)
		upstream, err := validateConfig(context.Background(), appCfg, cfg, logger)
		if err != nil {
			return err
		}
		printConfigValidationResult(cmd.ConfigPath, cfg, upstream)
		return nil
	case "show":
		if cmd.ConfigPath != "" && cmd.NameRef != "" {
			return fmt.Errorf("ambiguous config identity: provide either --config or --name, not both")
		}
		if cmd.ConfigPath == "" && cmd.NameRef == "" {
			return fmt.Errorf("missing config or name. Use: mirror config show -c <config_file> or mirror config show -n <mirror_name>")
		}
		if cmd.ConfigPath == "" {
			cfg, err := state.LoadMirrorConfig(appCfg.DBPath(cmd.NameRef))
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

func runConfigDrivenMirrorCommand(cmd cli.Command, appCfg appconfig.Config) error {
	return runConfigDrivenMirrorCommandWithLogger(cmd, appCfg, logging.Nop())
}

func runConfigDrivenMirrorCommandWithLogger(cmd cli.Command, appCfg appconfig.Config, logger logging.Logger) error {
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

	service, err := newMirrorServiceWithLogger(appCfg, logger)
	if err != nil {
		return err
	}

	switch cmd.Name {
	case "create":
		return runUpdateWorkflow("Create", service, appCfg, cfg, logger)
	case "fetch":
		result, err := service.Fetch(context.Background(), cfg)
		if err != nil {
			return err
		}
		printFetchResult("Fetch", result)
		return nil
	default:
		return notImplemented(cmd.Name)
	}
}

func runUpdateCommandWithLogger(cmd cli.Command, appCfg appconfig.Config, logger logging.Logger) error {
	cfg, err := refreshedUpdateConfig(cmd, appCfg, logger)
	if err != nil {
		return err
	}
	if cfg.UpdatePolicy == "never" {
		return updateDisabledError(cfg.Name)
	}
	service, err := newMirrorServiceWithLogger(appCfg, logger)
	if err != nil {
		return err
	}
	return runUpdateWorkflow("Update", service, appCfg, cfg, logger)
}

func runPublishUpdate(action string, mirrorService *mirror.Service, appCfg appconfig.Config, cfg config.Mirror) error {
	return runPublishUpdateWithLogger(action, mirrorService, appCfg, cfg, logging.Nop())
}

func runPublishUpdateWithLogger(action string, mirrorService *mirror.Service, appCfg appconfig.Config, cfg config.Mirror, logger logging.Logger) error {
	fetchResult, err := mirrorService.Fetch(context.Background(), cfg)
	if err != nil {
		return err
	}
	snapshotService, err := snapshot.NewService(snapshot.WithDBDir(appCfg.DBDir()))
	if err != nil {
		return err
	}
	updateResult, err := snapshotService.CreateCurrent(cfg)
	if err != nil {
		return err
	}
	publishService, err := publish.NewService(publish.WithStorageDirs(appCfg.DBDir(), appCfg.PackageDir(), appCfg.MirrorsRoot))
	if err != nil {
		return err
	}
	publishResult, err := publishService.PublishSelected(cfg)
	if err != nil {
		return err
	}
	signResult, err := signPublishedWithLogger(context.Background(), cfg, publishResult, logger)
	if err != nil {
		return err
	}
	printFetchResult(action+" fetch", fetchResult)
	printUpdateResult(updateResult)
	printPublishResult(publishResult)
	printSigningResult(signResult)
	return nil
}

func runMirrorCommand(cmd cli.Command) error {
	appCfg, err := loadAppConfig()
	if err != nil {
		return err
	}
	return runMirrorCommandWithConfig(cmd, appCfg)
}

func runMirrorCommandWithConfig(cmd cli.Command, appCfg appconfig.Config) error {
	return runMirrorCommandWithConfigAndLogger(cmd, appCfg, logging.Nop())
}

func runMirrorCommandWithConfigAndLogger(cmd cli.Command, appCfg appconfig.Config, logger logging.Logger) error {
	if cmd.Name == "daily" || cmd.Name == "weekly" || cmd.Name == "monthly" {
		return runPeriodicBatchCommandWithLogger(cmd.Name, appCfg, logger)
	}
	if err := validateMirrorIdentity(cmd); err != nil {
		return err
	}
	name, err := mirrorNameFromCommand(cmd)
	if err != nil {
		return err
	}
	service, err := newMirrorServiceWithLogger(appCfg, logger)
	if err != nil {
		return err
	}
	switch cmd.Name {
	case "rollback":
		snapshotService, err := snapshot.NewService(snapshot.WithDBDir(appCfg.DBDir()))
		if err != nil {
			return err
		}
		result, err := snapshotService.Rollback(name, cmd.Date, cmd.ID)
		if err != nil {
			return err
		}
		printRollbackResult(result)
		cfg, err := configForMirrorCommand(cmd, appCfg, name)
		if err != nil {
			return err
		}
		publishService, err := publish.NewService(publish.WithStorageDirs(appCfg.DBDir(), appCfg.PackageDir(), appCfg.MirrorsRoot))
		if err != nil {
			return err
		}
		publishResult, err := publishService.PublishSelected(cfg)
		if err != nil {
			return err
		}
		signResult, err := signPublishedWithLogger(context.Background(), cfg, publishResult, logger)
		if err != nil {
			return err
		}
		printPublishResult(publishResult)
		printSigningResult(signResult)
		return nil
	case "info":
		return runInfo(cmd, service, appCfg, name)
	case "more-info":
		return runMoreInfoWithConfig(name, service, appCfg)
	case "destroy":
		result, err := runDestroyWithConfigAndLogger(name, appCfg, logger)
		if err != nil {
			return err
		}
		printDestroyResult(result)
		return nil
	case "hide":
		publishService, err := publish.NewService(publish.WithStorageDirs(appCfg.DBDir(), appCfg.PackageDir(), appCfg.MirrorsRoot))
		if err != nil {
			return err
		}
		result, err := publishService.Hide(name)
		if err != nil {
			return err
		}
		printPublishResult(result)
		return nil
	case "cleanup":
		result, err := runCleanupWithConfigAndLogger(name, cmd, appCfg, logger)
		if err != nil {
			return err
		}
		printCleanupResult(result)
		return nil
	}
	return notImplemented(cmd.Name)
}

func runList(cmd cli.Command) error {
	appCfg, err := loadAppConfig()
	if err != nil {
		return err
	}
	return runListWithConfig(cmd, appCfg)
}

func runListWithConfig(cmd cli.Command, appCfg appconfig.Config) error {
	return runListWithConfigAndLogger(cmd, appCfg, logging.Nop())
}

func runListWithConfigAndLogger(cmd cli.Command, appCfg appconfig.Config, logger logging.Logger) error {
	if cmd.Subcommand != "" {
		return fmt.Errorf("list does not accept subcommands")
	}
	service, err := newMirrorServiceWithLogger(appCfg, logger)
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

var implementationTargets = map[string]implementationTarget{}

func newMirrorService(appCfg appconfig.Config) (*mirror.Service, error) {
	return newMirrorServiceWithLogger(appCfg, logging.Nop())
}

func newMirrorServiceWithLogger(appCfg appconfig.Config, logger logging.Logger) (*mirror.Service, error) {
	return mirror.NewService(
		mirror.WithStorageDirs(appCfg.DBDir(), appCfg.PackageDir()),
		mirror.WithDownloader(appCfg.NewDownloaderWithLogger(logger)),
		mirror.WithDownloadPlanReporter(printDownloadPlan),
		mirror.WithDownloadThreads(appCfg.DownloadThreads),
		mirror.WithProgressReporter(newTerminalProgressReporter()),
		mirror.WithLogger(logger),
	)
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
	if cmd.URL != "" {
		return "", nil
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

func validateMirrorIdentity(cmd cli.Command) error {
	identities := 0
	if cmd.ConfigPath != "" {
		identities++
	}
	if cmd.NameRef != "" {
		identities++
	}
	if cmd.URL != "" {
		identities++
	}
	if identities > 1 {
		if cmd.Name == "info" {
			return fmt.Errorf("ambiguous mirror identity: provide only one of --config, --name, or --URL")
		}
		return fmt.Errorf("ambiguous mirror identity: provide either --config or --name, not both")
	}
	if identities == 0 {
		if cmd.Name == "info" {
			return fmt.Errorf("missing mirror identity. Use --config <config_file>, --name <mirror_name>, or --URL <repo_url>")
		}
		return fmt.Errorf("missing mirror identity. Use --config <config_file> or --name <mirror_name>")
	}
	if cmd.URL != "" && cmd.Name != "info" {
		return fmt.Errorf("%s does not accept --URL; use --config or --name", cmd.Name)
	}
	return nil
}

func configForMirrorCommand(cmd cli.Command, appCfg appconfig.Config, name string) (config.Mirror, error) {
	if cmd.ConfigPath != "" {
		cfg, err := config.Load(cmd.ConfigPath)
		if err != nil {
			return config.Mirror{}, err
		}
		if err := config.Validate(cfg); err != nil {
			return config.Mirror{}, err
		}
		return cfg, nil
	}
	return state.LoadMirrorConfig(appCfg.DBPath(name))
}

func refreshedUpdateConfig(cmd cli.Command, appCfg appconfig.Config, logger logging.Logger) (config.Mirror, error) {
	if cmd.ConfigPath != "" && cmd.NameRef != "" {
		return config.Mirror{}, fmt.Errorf("ambiguous mirror identity: provide either --config or --name, not both")
	}
	if cmd.ConfigPath == "" && cmd.NameRef == "" {
		return config.Mirror{}, fmt.Errorf("missing mirror identity. Use --config <config_file> or --name <mirror_name>")
	}
	if cmd.URL != "" {
		return config.Mirror{}, fmt.Errorf("update does not accept --URL; use --config or --name")
	}
	if cmd.ConfigPath != "" {
		cfg, err := config.Load(cmd.ConfigPath)
		if err != nil {
			return config.Mirror{}, err
		}
		if err := config.Validate(cfg); err != nil {
			return config.Mirror{}, err
		}
		if err := saveMirrorConfigWithConfig(cfg, appCfg); err != nil {
			return config.Mirror{}, err
		}
		logger.Infof("config refreshed mirror=%q config_path=%q source=command", cfg.Name, cfg.ConfigPath)
		return state.LoadMirrorConfig(appCfg.DBPath(cfg.Name))
	}
	return refreshMirrorConfigByName(cmd.NameRef, appCfg, logger)
}

func refreshMirrorConfigByName(name string, appCfg appconfig.Config, logger logging.Logger) (config.Mirror, error) {
	store, err := state.Open(appCfg.DBPath(name))
	if err != nil {
		return config.Mirror{}, err
	}
	stored, err := store.MirrorConfig()
	if err != nil {
		_ = store.Close()
		return config.Mirror{}, err
	}
	if strings.TrimSpace(stored.ConfigPath) == "" {
		_ = store.Close()
		return config.Mirror{}, fmt.Errorf("mirror %q has no stored config path; recreate it with --config before DB-backed workflows can refresh it", name)
	}
	refreshed, err := config.Load(stored.ConfigPath)
	if err != nil {
		_ = store.Close()
		return config.Mirror{}, fmt.Errorf("refresh mirror %q config %q: %w", name, stored.ConfigPath, err)
	}
	if err := config.Validate(refreshed); err != nil {
		_ = store.Close()
		return config.Mirror{}, fmt.Errorf("refresh mirror %q config %q: %w", name, stored.ConfigPath, err)
	}
	if refreshed.Name != name {
		_ = store.Close()
		return config.Mirror{}, fmt.Errorf("refresh mirror %q config %q: config name changed to %q", name, stored.ConfigPath, refreshed.Name)
	}
	if err := store.SaveMirrorConfig(refreshed); err != nil {
		_ = store.Close()
		return config.Mirror{}, err
	}
	cfg, err := store.MirrorConfig()
	closeErr := store.Close()
	if err != nil {
		return config.Mirror{}, err
	}
	if closeErr != nil {
		return config.Mirror{}, closeErr
	}
	logger.Infof("config refreshed mirror=%q config_path=%q source=db", cfg.Name, cfg.ConfigPath)
	return cfg, nil
}

func saveMirrorConfigWithConfig(cfg config.Mirror, appCfg appconfig.Config) error {
	store, err := state.Open(appCfg.DBPath(cfg.Name))
	if err != nil {
		return err
	}
	defer func() {
		_ = store.Close()
	}()
	return store.SaveMirrorConfig(cfg)
}

func updateDisabledError(name string) error {
	return fmt.Errorf("update skipped for mirror %q: update policy is never; remove or change update = never in the config file to allow updates", name)
}

func validateConfigWorkflow(ctx context.Context, appCfg appconfig.Config, cfg config.Mirror) ([]config.UpstreamRelease, error) {
	return validateConfigWorkflowWithLogger(ctx, appCfg, cfg, logging.Nop())
}

func validateConfigWorkflowWithLogger(ctx context.Context, appCfg appconfig.Config, cfg config.Mirror, logger logging.Logger) ([]config.UpstreamRelease, error) {
	if err := config.Validate(cfg); err != nil {
		return nil, err
	}
	if err := validateExistingMirrorConfigWithConfig(cfg, appCfg); err != nil {
		return nil, err
	}
	logger.Debugf("validate upstream mirror=%q url=%q", cfg.Name, cfg.URL)
	return config.ValidateUpstreamDetails(ctx, cfg, appCfg.NewDownloaderWithLogger(logger))
}

func validateExistingMirrorConfig(cfg config.Mirror) error {
	appCfg, err := loadAppConfig()
	if err != nil {
		return err
	}
	return validateExistingMirrorConfigWithConfig(cfg, appCfg)
}

func validateExistingMirrorConfigWithConfig(cfg config.Mirror, appCfg appconfig.Config) error {
	dbPath := appCfg.DBPath(cfg.Name)
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	store, err := state.Open(dbPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = store.Close()
	}()
	stored, err := store.MirrorConfig()
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if sameConfigPath(stored.ConfigPath, cfg.ConfigPath) || mirrorConfigIdentityEquivalent(stored, cfg) {
		return nil
	}
	return fmt.Errorf("mirror %q already exists in %s with different config values", cfg.Name, dbPath)
}

func sameConfigPath(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	return left != "" && right != "" && left == right
}

func mirrorConfigIdentityEquivalent(left, right config.Mirror) bool {
	return left.Name == right.Name &&
		left.URL == right.URL &&
		stringSlicesEqual(left.Dists, right.Dists) &&
		stringSlicesEqual(left.Releases, right.Releases) &&
		left.Origin == right.Origin &&
		left.Label == right.Label &&
		stringSlicesEqual(left.Arch, right.Arch) &&
		stringSlicesEqual(left.Components, right.Components) &&
		left.Path == right.Path
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func periodicShouldRun(name, command string) (bool, string, error) {
	appCfg, err := loadAppConfig()
	if err != nil {
		return false, "", err
	}
	return periodicShouldRunWithConfig(name, command, appCfg)
}

func periodicShouldRunWithConfig(name, command string, appCfg appconfig.Config) (bool, string, error) {
	published, err := loadPublishedWithConfig(name, appCfg)
	if errors.Is(err, sql.ErrNoRows) {
		return true, "", nil
	}
	if err != nil {
		return false, "", err
	}
	if published.Hidden {
		return true, "", nil
	}
	return periodicShouldRunForPublished(name, command, published, time.Now())
}

func periodicShouldRunForPublished(name, command string, published state.PublishedRecord, now time.Time) (bool, string, error) {
	if command != "daily" && command != "weekly" && command != "monthly" {
		return true, "", nil
	}
	date := snapshotDate(published.SnapshotName)
	if date == "" {
		return true, "", nil
	}
	publishedDate, err := time.Parse("2006-01-02", date)
	if err != nil {
		return true, "", nil
	}
	today := localDate(now)
	if command == "monthly" {
		dueDate := nextCalendarMonthDate(publishedDate)
		if today.Before(dueDate) {
			return false, fmt.Sprintf("Monthly skipped for mirror %q: published snapshot %s is not at least one calendar month old, due on %s", name, published.SnapshotName, dueDate.Format("2006-01-02")), nil
		}
		return true, "", nil
	}

	thresholds := map[string]int{
		"daily":  1,
		"weekly": 7,
	}
	threshold := thresholds[command]
	ageDays := int(today.Sub(publishedDate).Hours() / 24)
	if ageDays < threshold {
		return false, fmt.Sprintf("%s skipped for mirror %q: published snapshot %s is %d day(s) old, threshold is %d day(s)", title(command), name, published.SnapshotName, ageDays, threshold), nil
	}
	return true, "", nil
}

func localDate(now time.Time) time.Time {
	parsed, _ := time.Parse("2006-01-02", now.Local().Format("2006-01-02"))
	return parsed
}

func nextCalendarMonthDate(date time.Time) time.Time {
	year, month, day := date.Date()
	month++
	if month > 12 {
		month = 1
		year++
	}
	maxDay := daysInMonth(year, month)
	if day > maxDay {
		day = maxDay
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func runPeriodicBatchCommandWithLogger(command string, appCfg appconfig.Config, logger logging.Logger) error {
	service, err := newMirrorServiceWithLogger(appCfg, logger)
	if err != nil {
		return err
	}
	names, err := periodicMirrorNames(appCfg.DBDir())
	if err != nil {
		return err
	}
	if len(names) == 0 {
		message := fmt.Sprintf("%s skipped: no mirrors found", title(command))
		reportPeriodicSkip(logger, message)
		return nil
	}

	failures := 0
	for _, name := range names {
		cfg, err := refreshMirrorConfigForPeriodic(name, appCfg, logger)
		if err != nil {
			reportPeriodicFailure(logger, command, name, err)
			if isBatchFatalDiskError(err) {
				return err
			}
			failures++
			continue
		}

		published, err := loadPublishedForPeriodic(name, appCfg)
		if errors.Is(err, sql.ErrNoRows) {
			reportPeriodicSkip(logger, fmt.Sprintf("%s skipped for mirror %q: mirror is not published", title(command), name))
			continue
		}
		if err != nil {
			reportPeriodicFailure(logger, command, name, err)
			if isBatchFatalDiskError(err) {
				return err
			}
			failures++
			continue
		}
		if published.Hidden {
			reportPeriodicSkip(logger, fmt.Sprintf("%s skipped for mirror %q: mirror is hidden/unpublished", title(command), name))
			continue
		}

		if cfg.UpdatePolicy == "" {
			reportPeriodicSkip(logger, fmt.Sprintf("%s skipped for mirror %q: no update policy configured", title(command), name))
			continue
		}
		if cfg.UpdatePolicy == "never" {
			reportPeriodicSkip(logger, fmt.Sprintf("%s skipped for mirror %q: update policy is never", title(command), name))
			continue
		}
		if cfg.UpdatePolicy != command {
			reportPeriodicSkip(logger, fmt.Sprintf("%s skipped for mirror %q: update policy is %s", title(command), name, cfg.UpdatePolicy))
			continue
		}

		shouldRun, message, err := periodicShouldRunForPublished(name, command, published, time.Now())
		if err != nil {
			failures++
			reportPeriodicFailure(logger, command, name, err)
			continue
		}
		if !shouldRun {
			reportPeriodicSkip(logger, message)
			continue
		}

		if err := runUpdateWorkflow(title(command), service, appCfg, cfg, logger); err != nil {
			reportPeriodicFailure(logger, command, name, err)
			if isBatchFatalDiskError(err) {
				return err
			}
			failures++
			continue
		}
	}
	if failures > 0 {
		return fmt.Errorf("%s completed with %d mirror failure(s)", command, failures)
	}
	return nil
}

func periodicMirrorNames(dbDir string) ([]string, error) {
	entries, err := os.ReadDir(dbDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sqlite" {
			continue
		}
		names = append(names, strings.TrimSuffix(entry.Name(), ".sqlite"))
	}
	sort.Strings(names)
	return names, nil
}

func loadPublishedWithConfig(name string, appCfg appconfig.Config) (state.PublishedRecord, error) {
	store, err := state.Open(appCfg.DBPath(name))
	if err != nil {
		return state.PublishedRecord{}, err
	}
	defer func() {
		_ = store.Close()
	}()
	return store.Published()
}

func reportPeriodicSkip(logger logging.Logger, message string) {
	fmt.Println(message)
	logger.Infof("%s", message)
}

func reportPeriodicFailure(logger logging.Logger, command, name string, err error) {
	message := fmt.Sprintf("%s failed for mirror %q: %v", title(command), name, err)
	fmt.Println(message)
	logger.Errorf("%s", message)
}

func isBatchFatalDiskError(err error) bool {
	if errors.Is(err, mirror.ErrInsufficientDiskSpace) || errors.Is(err, syscall.ENOSPC) {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no space left on device") || strings.Contains(text, "not enough disk space")
}

func runInfo(cmd cli.Command, service *mirror.Service, appCfg appconfig.Config, name string) error {
	if cmd.URL != "" {
		matches, err := summariesByURL(service, cmd.URL)
		if err != nil {
			return err
		}
		if len(matches) == 0 {
			return fmt.Errorf("no mirrors found for URL %q", cmd.URL)
		}
		for index, summary := range matches {
			if index > 0 {
				fmt.Println()
			}
			printSummary(summary)
			if cmd.Snapshot != "" {
				snapshotService, err := snapshot.NewService(snapshot.WithDBDir(appCfg.DBDir()))
				if err != nil {
					return err
				}
				snapshotSummary, err := snapshotService.Snapshot(summary.Config.Name, cmd.Snapshot)
				if err != nil {
					return err
				}
				printSnapshotSummary(snapshotSummary)
			}
		}
		return nil
	}
	summary, err := service.Info(name)
	if err != nil {
		return err
	}
	printSummary(summary)
	snapshotService, err := snapshot.NewService(snapshot.WithDBDir(appCfg.DBDir()))
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
}

func summariesByURL(service *mirror.Service, rawURL string) ([]mirror.Summary, error) {
	summaries, err := service.List()
	if err != nil {
		return nil, err
	}
	target := normalizeURL(rawURL)
	var matches []mirror.Summary
	for _, summary := range summaries {
		if normalizeURL(summary.Config.URL) == target {
			matches = append(matches, summary)
		}
	}
	return matches, nil
}

func normalizeURL(rawURL string) string {
	return strings.TrimRight(strings.TrimSpace(rawURL), "/") + "/"
}

type destroyResult struct {
	MirrorName      string
	DBPath          string
	PublishedPath   string
	PackageFiles    int
	PackageBytes    int64
	SharedPreserved int
}

func runDestroy(name string) (destroyResult, error) {
	appCfg, err := loadAppConfig()
	if err != nil {
		return destroyResult{}, err
	}
	return runDestroyWithConfig(name, appCfg)
}

func runDestroyWithConfig(name string, appCfg appconfig.Config) (destroyResult, error) {
	return runDestroyWithConfigAndLogger(name, appCfg, logging.Nop())
}

func runDestroyWithConfigAndLogger(name string, appCfg appconfig.Config, logger logging.Logger) (destroyResult, error) {
	dbPath := appCfg.DBPath(name)
	logger.Infof("destroy start mirror=%q db_path=%q", name, dbPath)
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return destroyResult{}, fmt.Errorf("mirror %q does not exist", name)
		}
		return destroyResult{}, err
	}
	store, err := state.Open(dbPath)
	if err != nil {
		return destroyResult{}, err
	}
	cfg, err := store.MirrorConfig()
	if err != nil {
		_ = store.Close()
		return destroyResult{}, err
	}
	packages, err := store.AllPackages()
	if err != nil {
		_ = store.Close()
		return destroyResult{}, err
	}
	_ = store.Close()

	result := destroyResult{MirrorName: name, DBPath: dbPath}
	publishedPath, err := destroyPublishPath(appCfg.MirrorsRoot, cfg.Path)
	if err != nil {
		return destroyResult{}, err
	}
	result.PublishedPath = publishedPath
	if publishedPath != "" {
		logger.Warnf("destroy removed published_path=%q mirror=%q", publishedPath, name)
	}

	packagePool, err := pool.New(appCfg.PackageDir())
	if err != nil {
		return destroyResult{}, err
	}
	for _, poolPath := range uniquePoolPaths(packages) {
		shared, err := packageReferencedByOtherMirror(appCfg.DBDir(), cfg.Name, poolPath)
		if err != nil {
			return destroyResult{}, err
		}
		if shared {
			result.SharedPreserved++
			logger.Debugf("destroy preserved shared package pool_path=%q mirror=%q", poolPath, name)
			continue
		}
		logger.Warnf("destroy removing package pool_path=%q mirror=%q", poolPath, name)
		size, err := packagePool.Remove(poolPath)
		if err != nil {
			if !os.IsNotExist(err) {
				return destroyResult{}, err
			}
			size = 0
		}
		result.PackageFiles++
		result.PackageBytes += size
	}
	service, err := newMirrorServiceWithLogger(appCfg, logger)
	if err != nil {
		return destroyResult{}, err
	}
	if err := service.Destroy(name); err != nil {
		return destroyResult{}, err
	}
	logger.Infof("destroy complete mirror=%q package_files=%d package_bytes=%d shared_preserved=%d", name, result.PackageFiles, result.PackageBytes, result.SharedPreserved)
	return result, nil
}

func destroyPublishPath(home, rawPath string) (string, error) {
	if strings.TrimSpace(rawPath) == "" {
		return "", nil
	}
	root := rawPath
	if !filepath.IsAbs(root) {
		root = filepath.Join(home, filepath.Clean(root))
	}
	clean := filepath.Clean(root)
	if clean == "." || clean == string(filepath.Separator) || clean == home {
		return "", fmt.Errorf("refusing to remove unsafe publish path %q", rawPath)
	}
	if err := os.RemoveAll(clean); err != nil {
		return "", err
	}
	return clean, nil
}

func uniquePoolPaths(packages []state.PackageRecord) []string {
	seen := map[string]bool{}
	var paths []string
	for _, pkg := range packages {
		if strings.TrimSpace(pkg.PoolPath) == "" || seen[pkg.PoolPath] {
			continue
		}
		seen[pkg.PoolPath] = true
		paths = append(paths, pkg.PoolPath)
	}
	sort.Strings(paths)
	return paths
}

func packageReferencedByOtherMirror(dbDir, mirrorName, poolPath string) (bool, error) {
	entries, err := os.ReadDir(dbDir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sqlite" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".sqlite")
		if name == mirrorName {
			continue
		}
		store, err := state.Open(filepath.Join(dbDir, name+".sqlite"))
		if err != nil {
			return false, err
		}
		packages, err := store.AllPackages()
		_ = store.Close()
		if err != nil {
			return false, err
		}
		for _, pkg := range packages {
			if pkg.PoolPath == poolPath {
				return true, nil
			}
		}
	}
	return false, nil
}

func runMoreInfo(name string, service *mirror.Service) error {
	appCfg, err := loadAppConfig()
	if err != nil {
		return err
	}
	return runMoreInfoWithConfig(name, service, appCfg)
}

func runMoreInfoWithConfig(name string, service *mirror.Service, appCfg appconfig.Config) error {
	store, err := state.Open(appCfg.DBPath(name))
	if err != nil {
		return err
	}
	defer func() {
		_ = store.Close()
	}()
	packages, err := store.AllPackages()
	if err != nil {
		return err
	}
	printMoreInfoPackages(packages)
	return nil
}

func userHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

type cleanupResult struct {
	MirrorName         string
	DBPath             string
	Mode               string
	CutoffDate         string
	Remove             bool
	Snapshots          []cleanupSnapshot
	SnapshotCandidates []string
	SnapshotsRemoved   int
	PackageCandidates  []string
	PackagesRemoved    int
	BytesRemoved       int64
	PublishedSnapshot  string
}

type cleanupSnapshot struct {
	Name         string
	Kind         string
	CreatedAt    time.Time
	PackageCount int
	SizeBytes    int64
}

func runCleanup(name string, cmd cli.Command) (cleanupResult, error) {
	appCfg, err := loadAppConfig()
	if err != nil {
		return cleanupResult{}, err
	}
	return runCleanupWithConfig(name, cmd, appCfg)
}

func runCleanupWithConfig(name string, cmd cli.Command, appCfg appconfig.Config) (cleanupResult, error) {
	return runCleanupWithConfigAndLogger(name, cmd, appCfg, logging.Nop())
}

func runCleanupWithConfigAndLogger(name string, cmd cli.Command, appCfg appconfig.Config, logger logging.Logger) (cleanupResult, error) {
	dbPath := appCfg.DBPath(name)
	logger.Infof("cleanup start mirror=%q mode=%q db_path=%q", name, cleanupMode(cmd), dbPath)
	store, err := state.Open(dbPath)
	if err != nil {
		return cleanupResult{}, err
	}
	defer func() {
		_ = store.Close()
	}()

	result := cleanupResult{
		MirrorName: name,
		DBPath:     dbPath,
		Mode:       cleanupMode(cmd),
		Remove:     cmd.CleanupDaysSet || cmd.CleanupAll,
	}

	publishedDate := ""
	published, err := store.Published()
	if err == nil {
		result.PublishedSnapshot = published.SnapshotName
		publishedDate = snapshotDate(published.SnapshotName)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return cleanupResult{}, err
	}

	snapshots, err := store.Snapshots()
	if err != nil {
		return cleanupResult{}, err
	}
	for _, item := range snapshots {
		packages, err := store.SnapshotPackages(item.Name)
		if err != nil {
			return cleanupResult{}, err
		}
		result.Snapshots = append(result.Snapshots, cleanupSnapshot{
			Name:         item.Name,
			Kind:         item.Kind,
			CreatedAt:    item.CreatedAt,
			PackageCount: len(packages),
			SizeBytes:    packageSize(packages),
		})
	}

	if cmd.CleanupDaysSet || cmd.CleanupAll {
		if result.PublishedSnapshot == "" {
			return cleanupResult{}, fmt.Errorf("cleanup %s requires a currently published snapshot", result.Mode)
		}
		if publishedDate == "" {
			return cleanupResult{}, fmt.Errorf("published snapshot %q does not contain a YYYY-MM-DD date suffix", result.PublishedSnapshot)
		}
		candidates, cutoff, err := cleanupSnapshotCandidates(snapshots, cmd, result.PublishedSnapshot, publishedDate)
		if err != nil {
			return cleanupResult{}, err
		}
		result.CutoffDate = cutoff
		result.SnapshotCandidates = candidates
		logger.Infof("cleanup snapshot candidates mirror=%q mode=%q count=%d cutoff=%q", name, result.Mode, len(candidates), cutoff)
		if len(result.SnapshotCandidates) > 0 {
			logger.Warnf("cleanup deleting snapshots mirror=%q count=%d", name, len(result.SnapshotCandidates))
			if err := store.DeleteSnapshots(result.SnapshotCandidates); err != nil {
				return cleanupResult{}, err
			}
			result.SnapshotsRemoved = len(result.SnapshotCandidates)
		}
	}

	paths, err := store.UnreferencedPoolPaths()
	if err != nil {
		return cleanupResult{}, err
	}
	result.PackageCandidates = paths
	logger.Infof("cleanup package candidates mirror=%q count=%d remove=%t", name, len(paths), result.Remove)
	if result.Remove && len(paths) > 0 {
		packagePool, err := pool.New(appCfg.PackageDir())
		if err != nil {
			return cleanupResult{}, err
		}
		for _, path := range paths {
			logger.Warnf("cleanup removing package pool_path=%q mirror=%q", path, name)
			size, err := packagePool.RemoveIfUnreferenced(path, store)
			if err != nil {
				if !os.IsNotExist(err) {
					return cleanupResult{}, err
				}
				size = 0
			}
			if err := store.DeleteUnreferencedPackage(path); err != nil {
				return cleanupResult{}, err
			}
			result.PackagesRemoved++
			result.BytesRemoved += size
		}
	}
	if result.Remove {
		_, err := store.RecordUpdateHistory(state.UpdateRecord{
			Action:     "cleanup",
			Status:     "ok",
			Message:    fmt.Sprintf("removed %d snapshot(s), %d package file(s)", result.SnapshotsRemoved, result.PackagesRemoved),
			StartedAt:  time.Now(),
			FinishedAt: time.Now(),
		})
		if err != nil {
			return cleanupResult{}, err
		}
	}
	logger.Infof("cleanup complete mirror=%q removed_snapshots=%d removed_packages=%d removed_bytes=%d", name, result.SnapshotsRemoved, result.PackagesRemoved, result.BytesRemoved)
	return result, nil
}

func cleanupMode(cmd cli.Command) string {
	if cmd.CleanupAll {
		return "all"
	}
	if cmd.CleanupDaysSet {
		return fmt.Sprintf("days=%d", cmd.CleanupDays)
	}
	return "summary"
}

func cleanupSnapshotCandidates(snapshots []state.SnapshotRecord, cmd cli.Command, publishedSnapshot, publishedDate string) ([]string, string, error) {
	var cutoff string
	if cmd.CleanupDaysSet {
		published, err := time.Parse("2006-01-02", publishedDate)
		if err != nil {
			return nil, "", err
		}
		cutoff = published.AddDate(0, 0, -cmd.CleanupDays).Format("2006-01-02")
	}

	var candidates []string
	preserved := cleanupPreservedSnapshots(publishedSnapshot)
	for _, item := range snapshots {
		itemDate := snapshotDate(item.Name)
		if preserved[item.Name] {
			continue
		}
		if cmd.CleanupAll {
			candidates = append(candidates, item.Name)
			continue
		}
		if itemDate != "" && itemDate < cutoff {
			candidates = append(candidates, item.Name)
		}
	}
	return candidates, cutoff, nil
}

func cleanupPreservedSnapshots(publishedSnapshot string) map[string]bool {
	preserved := map[string]bool{
		publishedSnapshot: true,
	}
	if strings.HasPrefix(publishedSnapshot, "MERGED-") {
		regular := strings.TrimPrefix(publishedSnapshot, "MERGED-")
		if regular != "" {
			preserved[regular] = true
		}
	}
	return preserved
}

func snapshotDate(name string) string {
	index := strings.LastIndex(name, "_")
	if index < 0 || index == len(name)-1 {
		return ""
	}
	value := name[index+1:]
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return ""
	}
	return value
}

func packageSize(packages []state.PackageRecord) int64 {
	var size int64
	seen := map[string]bool{}
	for _, pkg := range packages {
		key := pkg.PoolPath
		if key == "" {
			key = pkg.Key
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		size += pkg.Size
	}
	return size
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

func printDownloadPlan(plan mirror.DownloadPlan) {
	fmt.Printf("Download plan for mirror %q\n", plan.MirrorName)
	fmt.Printf("Package pool: %s\n", plan.PackagePoolRoot)
	fmt.Printf("Indexes considered: %d\n", plan.IndexesConsidered)
	fmt.Printf("Packages reused: %d\n", plan.PackagesReused)
	fmt.Printf("Packages to download: %d\n", plan.PackagesToDownload)
	fmt.Printf("Estimated download size: %s\n", humanSize(plan.EstimatedDownloadBytes))
	if plan.UnknownSizePackages > 0 {
		fmt.Printf("Packages with unknown size: %d\n", plan.UnknownSizePackages)
	}
	fmt.Printf("Available disk space: %s\n", humanSize(plan.AvailableBytes))
	for _, warning := range plan.Warnings {
		fmt.Printf("Warning: %s\n", warning)
	}
}

func printConfigValidationResult(path string, cfg config.Mirror, upstream []config.UpstreamRelease) {
	fmt.Printf("Config %q is valid for mirror %q\n", path, cfg.Name)
	if len(upstream) == 0 {
		return
	}
	fmt.Println("Upstream values:")
	for _, item := range upstream {
		fmt.Printf("- %s: origin = %s, label = %s\n", item.Suite, displayReleaseValue(item.Origin), displayReleaseValue(item.Label))
	}
}

func displayReleaseValue(value string) string {
	if value == "" {
		return "(empty)"
	}
	return value
}

func printSummary(summary mirror.Summary) {
	fmt.Printf("Mirror: %s\n", summary.Config.Name)
	fmt.Printf("DB path: %s\n", summary.DBPath)
	fmt.Printf("URL: %s\n", summary.Config.URL)
	fmt.Printf("Suites: %s\n", strings.Join(suites(summary.Config), ", "))
	fmt.Printf("Components: %s\n", strings.Join(summary.Config.Components, ", "))
	fmt.Printf("Architectures: %s\n", strings.Join(summary.Config.Arch, ", "))
	fmt.Printf("Packages: %d current, %d known\n", summary.Stats.MirrorPackageCount, summary.Stats.KnownPackageCount)
	fmt.Printf("Mirror size: %s\n", humanSize(summary.Stats.MirrorSizeBytes))
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
}

func printRollbackResult(result snapshot.RollbackResult) {
	fmt.Printf("Rollback selected snapshot for mirror %q\n", result.MirrorName)
	fmt.Printf("DB path: %s\n", result.DBPath)
	fmt.Printf("Selected snapshot: %s\n", result.SelectedSnapshot)
	if len(result.ResolvedSnapshots) > 1 {
		fmt.Printf("Resolved snapshot group: %s\n", strings.Join(result.ResolvedSnapshots, ", "))
	}
}

func printPublishResult(result publish.Result) {
	if result.Hidden {
		fmt.Printf("Published output hidden for mirror %q\n", result.MirrorName)
		fmt.Printf("Path: %s\n", result.Path)
		return
	}
	fmt.Printf("Published unsigned repository for mirror %q\n", result.MirrorName)
	fmt.Printf("Path: %s\n", result.Path)
	fmt.Printf("Suite: %s\n", result.Suite)
	fmt.Printf("Snapshots: %s\n", strings.Join(result.Snapshots, ", "))
	fmt.Printf("Packages: %d\n", result.Packages)
	fmt.Printf("Indexes: %d\n", result.Indexes)
}

func signPublished(ctx context.Context, cfg config.Mirror, result publish.Result) (signing.Result, error) {
	return signPublishedWithLogger(ctx, cfg, result, logging.Nop())
}

func signPublishedWithLogger(ctx context.Context, cfg config.Mirror, result publish.Result, logger logging.Logger) (signing.Result, error) {
	service := signing.NewService(signing.WithLogger(logger))
	return service.Sign(ctx, cfg, signing.Repository{
		Path:  result.Path,
		Suite: result.Suite,
	})
}

func printSigningResult(result signing.Result) {
	if !result.Enabled {
		fmt.Println("Signing: disabled")
		return
	}
	fmt.Println("Signing: complete")
	fmt.Printf("InRelease: %s\n", result.InRelease)
	fmt.Printf("Release.gpg: %s\n", result.ReleaseGPG)
}

func printCleanupResult(result cleanupResult) {
	fmt.Printf("Cleanup %s for mirror %q\n", result.Mode, result.MirrorName)
	fmt.Printf("DB path: %s\n", result.DBPath)
	if result.PublishedSnapshot != "" {
		fmt.Printf("Published snapshot preserved: %s\n", result.PublishedSnapshot)
	}
	fmt.Printf("Snapshots: %d\n", len(result.Snapshots))
	for _, item := range result.Snapshots {
		fmt.Printf("- %s (%s, %d packages, %d bytes, created %s)\n",
			item.Name,
			item.Kind,
			item.PackageCount,
			item.SizeBytes,
			item.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		)
	}
	if result.CutoffDate != "" {
		fmt.Printf("Snapshot cutoff: before %s\n", result.CutoffDate)
	}
	fmt.Printf("Snapshot candidates: %d\n", len(result.SnapshotCandidates))
	for _, name := range result.SnapshotCandidates {
		fmt.Printf("- %s\n", name)
	}
	fmt.Printf("Package candidates: %d\n", len(result.PackageCandidates))
	for _, path := range result.PackageCandidates {
		fmt.Printf("- %s\n", path)
	}
	if result.Remove {
		fmt.Printf("Removed snapshots: %d\n", result.SnapshotsRemoved)
		fmt.Printf("Removed packages: %d\n", result.PackagesRemoved)
		fmt.Printf("Removed bytes: %d\n", result.BytesRemoved)
	}
}

func printDestroyResult(result destroyResult) {
	fmt.Printf("Destroyed mirror %q\n", result.MirrorName)
	fmt.Printf("DB path: %s\n", result.DBPath)
	if result.PublishedPath != "" {
		fmt.Printf("Published path removed: %s\n", result.PublishedPath)
	}
	fmt.Printf("Package files removed: %d\n", result.PackageFiles)
	fmt.Printf("Package bytes removed: %d\n", result.PackageBytes)
	fmt.Printf("Shared package files preserved: %d\n", result.SharedPreserved)
}

func printMoreInfoPackages(packages []state.PackageRecord) {
	fmt.Printf("Known packages: %d\n", len(packages))
	for _, pkg := range packages {
		fmt.Printf("- %s %s %s\n", pkg.Name, pkg.Version, pkg.Architecture)
		fmt.Printf("  Pool location: %s\n", pkg.PoolPath)
		fmt.Printf("  Size: %s\n", humanSize(pkg.Size))
		fmt.Printf("  MD5: %s\n", pkg.MD5)
		fmt.Printf("  SHA1: %s\n", pkg.SHA1)
		fmt.Printf("  SHA256: %s\n", pkg.SHA256)
		fmt.Printf("  SHA512: %s\n", pkg.SHA512)
	}
}

func humanSize(size int64) string {
	if size < 0 {
		return "0 B"
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	value := float64(size)
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", size, units[unit])
	}
	if value >= 10 {
		return fmt.Sprintf("%.0f %s", value, units[unit])
	}
	return fmt.Sprintf("%.1f %s", value, units[unit])
}

func printSnapshotList(snapshots []snapshot.Summary) {
	if len(snapshots) == 0 {
		fmt.Println("Snapshot list: none")
		return
	}
	fmt.Println("Snapshot list:")
	for _, item := range snapshots {
		fmt.Printf("- %s (%s, %d packages (%s), created %s)\n",
			item.Record.Name,
			item.Record.Kind,
			item.PackageCount,
			humanSize(item.PackageSizeBytes),
			item.Record.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		)
	}
}

func printSnapshotSummary(item snapshot.Summary) {
	fmt.Printf("Snapshot: %s\n", item.Record.Name)
	fmt.Printf("Kind: %s\n", item.Record.Kind)
	fmt.Printf("Created: %s\n", item.Record.CreatedAt.Format("2006-01-02T15:04:05Z07:00"))
	fmt.Printf("Packages: %d\n", item.PackageCount)
	fmt.Printf("Size: %s\n", humanSize(item.PackageSizeBytes))
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
