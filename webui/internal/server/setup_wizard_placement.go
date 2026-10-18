package server

import (
	"context"
	"errors"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func (a *App) resolveRecommendedSetupStorage(ctx context.Context, session string, draft *setupDraft) error {
	client, ok := a.api.(interface {
		AdminRecommendedSetupStorage(context.Context, string) (api.RecommendedSetupPlacement, error)
	})
	if !ok {
		return errors.New("Recommended storage discovery is unavailable.")
	}
	placement, err := client.AdminRecommendedSetupStorage(ctx, session)
	if err != nil {
		return err
	}
	draft.Storage = setupStorageDraft{Type: placement.Storage.Type, Path: placement.Storage.Path, Device: placement.Storage.Device, MountPoint: placement.Storage.MountPoint, Complete: true}
	// Keep this server's schedule and retention; placement resolves only location.
	draft.Backups.Type, draft.Backups.Path, draft.Backups.Device, draft.Backups.MountPoint = placement.Backups.Type, placement.Backups.Path, placement.Backups.Device, placement.Backups.MountPoint
	draft.Backups.Source, draft.Backups.Username, draft.Backups.Domain = "", "", ""
	draft.Backups.Complete = true
	return nil
}
