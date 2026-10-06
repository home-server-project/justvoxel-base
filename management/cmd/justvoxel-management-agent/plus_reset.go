package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Plus uses the existing reviewed reset, operation journal and identity reset.
// Only its runtime/storage cleanup differs from normal JustVoxel.
var plusFactoryResetPaths = defaultPlusDeploymentPaths

func (s *server) factoryResetPlan(ctx context.Context) (adminFactoryResetPlanResponse, int) {
	if !s.plus {
		return authoritativeAdminFactoryResetPlan(ctx)
	}
	plan, err := planPlusFactoryReset(plusFactoryResetPaths())
	if err != nil {
		return adminFactoryResetPlanResponse{OK: false, Error: err.Error()}, http.StatusConflict
	}
	return plan, 0
}

var plusResetMounts = func() ([]string, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	var mounts []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 4 {
			mounts = append(mounts, strings.NewReplacer("\\040", " ", "\\011", "\t", "\\134", "\\").Replace(fields[4]))
		}
	}
	return mounts, nil
}

func plusResetSafePath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return errors.New("invalid Plus reset path")
	}
	for current := path; current != "/"; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return errors.New("Plus reset path cannot be inspected")
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Plus reset refuses a symlink or non-directory storage path")
		}
	}
	return nil
}

func planPlusFactoryReset(p plusDeploymentPaths) (adminFactoryResetPlanResponse, error) {
	mounts, err := plusResetMounts()
	if err != nil {
		return adminFactoryResetPlanResponse{}, errors.New("mount identity is unavailable")
	}
	for _, dir := range []string{p.Configuration, p.Preparation, p.UnitOverride} {
		if err := plusResetSafePath(dir); err != nil {
			return adminFactoryResetPlanResponse{}, err
		}
		for _, mount := range mounts {
			if mount == dir || strings.HasPrefix(mount, dir+"/") {
				return adminFactoryResetPlanResponse{}, errors.New("unmount nested Plus configuration filesystems before reset")
			}
		}
	}
	plan := adminFactoryResetPlanResponse{OK: true, SchemaVersion: "v1", Mode: "factory", Plus: true,
		DataScope: "none", DataAction: "none", BackupScope: "none", BackupAction: "none",
		ConfigBackupsAction: "delete", StorageLayoutAction: "preserve", ExternalStorageAction: "preserve", NetworkStorageAction: "preserve",
		AuthenticationAction: "reset_to_system", WebUIUsersAction: "delete", PasswordAction: "restore_default_and_expire", SessionsAction: "invalidate",
		Players: []string{}, Warnings: []string{"Reset stops the infrastructure and game containers belonging to this Plus installation. There is no automatic backup."}}
	// Hash configuration as one opaque value; never return secrets or file contents.
	hash := sha256.New()
	// Preparation can change before deployment; bind review to its metadata,
	// without returning a password-derived hash to the frontend.
	if info, err := os.Lstat(filepath.Join(p.Preparation, "setup.json")); err == nil {
		if !info.Mode().IsRegular() {
			return plan, errors.New("Plus setup preparation must be a regular file")
		}
		fmt.Fprintln(hash, "preparation", info.Size(), info.ModTime().UnixNano())
	} else if !os.IsNotExist(err) {
		return plan, errors.New("Plus preparation is unreadable")
	}
	for _, file := range []string{"compose.yaml", "stack.env", "deployed"} {
		path := filepath.Join(p.Configuration, file)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			fmt.Fprintln(hash, file, "absent")
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1024*1024 {
			return plan, errors.New("Plus configuration must contain regular bounded files before reset")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return plan, errors.New("Plus configuration is unreadable")
		}
		fmt.Fprintln(hash, file)
		hash.Write(data)
		if file == "stack.env" {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "PLUS_DATA_ROOT=") {
					if plan.DataPath != "" {
						return plan, errors.New("Plus data path is ambiguous")
					}
					plan.DataPath = strings.TrimPrefix(line, "PLUS_DATA_ROOT=")
				}
			}
			if plan.DataPath == "" {
				return plan, errors.New("Plus data path is missing")
			}
		}
	}
	if plan.DataPath != "" {
		if err := plusResetSafePath(plan.DataPath); err != nil {
			return plan, err
		}
		plan.DataScope, plan.DataAction = "external", "preserve"
		if plan.DataPath == p.DefaultData {
			mounted := false
			for _, mount := range mounts {
				if mount == plan.DataPath {
					mounted = true
				}
				// A custom mount above the default directory is also preserved.
				if mount != "/" && mount != "/var" && strings.HasPrefix(plan.DataPath, mount+"/") {
					mounted = true
				}
				if strings.HasPrefix(mount, plan.DataPath+"/") {
					return plan, errors.New("unmount nested filesystems inside Plus data before reset")
				}
			}
			if !mounted {
				plan.DataScope, plan.DataAction = "internal", "delete"
			}
		}
		if plan.DataAction == "preserve" {
			plan.Warnings = append(plan.Warnings, "Application data on the selected second drive is preserved. Fresh setup requires an empty application directory.")
		}
	}
	plan.RuntimeFingerprint = hex.EncodeToString(hash.Sum(nil))
	fingerprint, err := factoryResetPlanFingerprint(plan)
	plan.PlanFingerprint = fingerprint
	return plan, err
}

var plusResetContainerID = regexp.MustCompile(`^[a-f0-9]{12,64}$`)
var plusResetServerDirectory = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func removePlusGameContainers(ctx context.Context, p plusDeploymentPaths, root string) error {
	if root == "" {
		return nil
	}
	output, err := runPlusDeploymentCommand(ctx, "docker", []string{"ps", "--all", "--quiet", "--filter", "label=Service=Pterodactyl"}, nil)
	if err != nil {
		return errors.New("game containers cannot be inspected; reset stopped")
	}
	for _, id := range strings.Fields(string(output)) {
		if !plusResetContainerID.MatchString(id) {
			return errors.New("invalid Docker container identity")
		}
		data, err := runPlusDeploymentCommand(ctx, "docker", []string{"inspect", id}, nil)
		if err != nil {
			return errors.New("game container identity cannot be verified")
		}
		var containers []struct {
			Config struct{ Labels map[string]string }
			Mounts []struct{ Source string }
		}
		if json.Unmarshal(data, &containers) != nil || len(containers) != 1 {
			return errors.New("invalid game container inspection")
		}
		owned := false
		if containers[0].Config.Labels["Service"] == "Pterodactyl" {
			for _, mount := range containers[0].Mounts {
				if filepath.Dir(mount.Source) == filepath.Join(root, "wings", "data") && plusResetServerDirectory.MatchString(filepath.Base(mount.Source)) {
					owned = true
				}
			}
		}
		if owned {
			if _, err := runPlusDeploymentCommand(ctx, "docker", []string{"rm", "--force", id}, nil); err != nil {
				return errors.New("Plus game container could not be removed")
			}
		}
	}
	return nil
}

func applyPlusFactoryReset(ctx context.Context, p plusDeploymentPaths, expected adminFactoryResetPlanResponse) ([]byte, error) {
	lock, err := plusPreparationLock(p.Preparation)
	if err != nil {
		return nil, errors.New("Plus setup is running; reset stopped")
	}
	defer lock.Close()
	plan, err := planPlusFactoryReset(p)
	if err != nil || plan.PlanFingerprint != expected.PlanFingerprint {
		return nil, errors.New("Plus reset plan changed; review again")
	}
	if plusSetupUnitRunning(ctx) {
		return nil, errors.New("Plus setup is running; reset stopped")
	}
	if _, err := runPlusDeploymentCommand(ctx, "systemctl", []string{"disable", "--now", "justvoxel-plus-stack.service"}, nil); err != nil {
		return nil, errors.New("Plus stack could not be stopped")
	}
	if _, err := os.Lstat(filepath.Join(p.Configuration, "compose.yaml")); err == nil {
		if _, err = plusCompose(ctx, p, nil, "down", "--remove-orphans"); err != nil {
			return nil, errors.New("Plus infrastructure containers could not be removed")
		}
	}
	if err := removePlusGameContainers(ctx, p, plan.DataPath); err != nil {
		return nil, err
	}
	// Recheck paths and mounts after stopping; do not follow symlinks or erase a second drive.
	again, err := planPlusFactoryReset(p)
	if err != nil || again.PlanFingerprint != plan.PlanFingerprint {
		return nil, errors.New("Plus storage changed during reset; cleanup stopped")
	}
	if plan.DataAction == "delete" {
		if err := os.RemoveAll(plan.DataPath); err != nil {
			return nil, errors.New("internal Plus data cleanup failed")
		}
	}
	if err := os.RemoveAll(p.Configuration); err != nil {
		return nil, errors.New("Plus configuration cleanup failed")
	}
	if err := os.RemoveAll(p.UnitOverride); err != nil {
		return nil, errors.New("Plus storage dependency cleanup failed")
	}
	for _, name := range []string{"setup.json", "deployment.json"} {
		if err := os.Remove(filepath.Join(p.Preparation, name)); err != nil && !os.IsNotExist(err) {
			return nil, errors.New("Plus setup state cleanup failed")
		}
	}
	if _, err := runPlusDeploymentCommand(ctx, "systemctl", []string{"daemon-reload"}, nil); err != nil {
		return nil, errors.New("Plus unit reload failed")
	}
	return json.Marshal(adminFactoryResetHelperApplyResponse{OK: true, SchemaVersion: "v1", Mode: "factory", DataPath: plan.DataPath, DataScope: plan.DataScope, DataAction: plan.DataAction, BackupScope: "none", BackupAction: "none", ConfigBackupsAction: "delete", StorageLayoutAction: "preserve", ExternalStorageAction: "preserve", NetworkStorageAction: "preserve"})
}

func (s *server) plusResetBlocksSetup(w http.ResponseWriter, r *http.Request) bool {
	if s.operations == nil {
		return false
	}
	current, err := s.operations.currentFactoryReset()
	if err != nil || current != nil {
		writeError(w, http.StatusConflict, "resolve the factory reset before starting setup")
		return true
	}
	return false
}
