package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mirrors/internal/appconfig"
	"mirrors/internal/config"
	"mirrors/internal/logging"
	"mirrors/internal/mirror"
	"mirrors/internal/publish"
	"mirrors/internal/signing"
	"mirrors/internal/snapshot"
	"mirrors/internal/state"
)

type workflowState struct {
	action           string
	appCfg           appconfig.Config
	cfg              config.Mirror
	logger           logging.Logger
	mirrorService    *mirror.Service
	fetchResult      mirror.FetchResult
	updateResult     snapshot.UpdateResult
	rollbackResult   snapshot.RollbackResult
	publishResult    publish.Result
	signingResult    signing.Result
	selectedSnapshot string
	startedAt        time.Time
}

type workflowEvent struct {
	name string
	run  func(context.Context, *workflowState) error
}

type workflowPublisher interface {
	PublishSnapshot(config.Mirror, string) (publish.Result, error)
}

var newWorkflowPublisher = func(appCfg appconfig.Config) (workflowPublisher, error) {
	return publish.NewService(publish.WithStorageDirs(appCfg.DBDir(), appCfg.PackageDir(), appCfg.MirrorsRoot))
}

func runWorkflowEvents(ctx context.Context, wf *workflowState, events []workflowEvent) error {
	if wf.startedAt.IsZero() {
		wf.startedAt = time.Now()
	}
	for _, event := range events {
		wf.logger.Infof("workflow event start action=%q event=%q mirror=%q", wf.action, event.name, wf.cfg.Name)
		if err := event.run(ctx, wf); err != nil {
			wf.logger.Errorf("workflow event failed action=%q event=%q mirror=%q error=%v", wf.action, event.name, wf.cfg.Name, err)
			return err
		}
		wf.logger.Infof("workflow event complete action=%q event=%q mirror=%q", wf.action, event.name, wf.cfg.Name)
	}
	return nil
}

func eventFetch() workflowEvent {
	return workflowEvent{
		name: "fetch",
		run: func(ctx context.Context, wf *workflowState) error {
			result, err := wf.mirrorService.Fetch(ctx, wf.cfg)
			if err != nil {
				return err
			}
			wf.fetchResult = result
			return nil
		},
	}
}

func eventCreateSnapshot() workflowEvent {
	return workflowEvent{
		name: "snapshot-create",
		run: func(_ context.Context, wf *workflowState) error {
			service, err := snapshot.NewService(snapshot.WithDBDir(wf.appCfg.DBDir()))
			if err != nil {
				return err
			}
			result, err := service.CreateCurrent(wf.cfg)
			if err != nil {
				return err
			}
			wf.updateResult = result
			wf.selectedSnapshot = result.SelectedSnapshot
			return nil
		},
	}
}

func eventSelectRollbackSnapshot(date, id string) workflowEvent {
	return workflowEvent{
		name: "snapshot-select-rollback",
		run: func(_ context.Context, wf *workflowState) error {
			service, err := snapshot.NewService(snapshot.WithDBDir(wf.appCfg.DBDir()))
			if err != nil {
				return err
			}
			result, err := service.Rollback(wf.cfg.Name, date, id)
			if err != nil {
				return err
			}
			wf.rollbackResult = result
			wf.selectedSnapshot = result.SelectedSnapshot
			return nil
		},
	}
}

func eventPublishSelectedSnapshot() workflowEvent {
	return workflowEvent{
		name: "publish",
		run: func(_ context.Context, wf *workflowState) error {
			service, err := newWorkflowPublisher(wf.appCfg)
			if err != nil {
				return err
			}
			result, err := service.PublishSnapshot(wf.cfg, wf.selectedSnapshot)
			if err != nil {
				return err
			}
			wf.publishResult = result
			return nil
		},
	}
}

func eventSignPublished() workflowEvent {
	return workflowEvent{
		name: "sign",
		run: func(ctx context.Context, wf *workflowState) error {
			result, err := signPublishedWithLogger(ctx, wf.cfg, wf.publishResult, wf.logger)
			if err != nil {
				return err
			}
			wf.signingResult = result
			return nil
		},
	}
}

func eventCommitPublishedState() workflowEvent {
	return workflowEvent{
		name: "state-commit",
		run: func(_ context.Context, wf *workflowState) error {
			if strings.TrimSpace(wf.selectedSnapshot) == "" {
				return fmt.Errorf("selected snapshot is required before state commit")
			}
			store, err := stateStoreForMirror(wf.appCfg, wf.cfg.Name)
			if err != nil {
				return err
			}
			defer func() {
				_ = store.Close()
			}()
			now := time.Now()
			if err := store.SetPublished(state.PublishedRecord{
				SnapshotName: wf.selectedSnapshot,
				Path:         wf.publishResult.Path,
				Suite:        wf.publishResult.Suite,
				Component:    firstWorkflowComponent(wf.cfg),
				PublishedAt:  now,
			}); err != nil {
				return err
			}
			_, err = store.RecordUpdateHistory(state.UpdateRecord{
				Action:     strings.ToLower(wf.action),
				Status:     "ok",
				Message:    fmt.Sprintf("published snapshot %s", wf.selectedSnapshot),
				StartedAt:  wf.startedAt,
				FinishedAt: now,
			})
			return err
		},
	}
}

func stateStoreForMirror(appCfg appconfig.Config, name string) (*state.Store, error) {
	return state.Open(appCfg.DBPath(name))
}

func firstWorkflowComponent(cfg config.Mirror) string {
	if len(cfg.Components) == 0 {
		return ""
	}
	return cfg.Components[0]
}
