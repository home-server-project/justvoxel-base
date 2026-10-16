package main

import (
	"context"
	"net/http"
	"os"
	"strings"
)

type instanceVersionKey struct{}
type instanceVersionSelectionKey struct{}

func validatedVersionInstance(w http.ResponseWriter, r *http.Request, live ...bool) (string, bool) {
	id, ok := instanceRequestID(w, r)
	if !ok {
		return "", false
	}
	record, err := instanceRuntimes.readRecord(id)
	if err == nil && (len(live) == 0 || live[0]) {
		err = instanceRuntimes.verifyLive(record)
	}
	if err != nil {
		instanceRuntimeError(w, err, id)
		return "", false
	}
	return id, true
}

// These bounded root-only commands are used by the mature update worker while
// it holds appliance maintenance. They expose no generic shell/container action.
func instanceVersionCommand(ctx context.Context, args []string) error {
	if os.Geteuid() != 0 || len(args) < 2 || !instanceIDPattern.MatchString(args[1]) {
		return errInstanceID
	}
	b := instanceRuntimes
	id := args[1]
	previous, err := b.readRecord(id)
	if err != nil {
		return err
	}
	if err = b.verifyLive(previous); err != nil {
		return err
	}
	switch args[0] {
	case "instance-version-backup":
		if len(args) != 2 {
			return errInstanceID
		}
		if _, err = b.backupStorage(ctx, "preflight", id); err != nil {
			return err
		}
		state, err := b.observe(ctx, id)
		if err != nil {
			return err
		}
		if state.State == "running" {
			if err = b.actionWithWarnings(ctx, id, "stop", true); err != nil {
				return err
			}
		} else if state.State != "stopped" {
			return errInstanceBusy
		}
		_, err = b.backupStorage(ctx, "create", id)
		return err
	case "instance-version-publish":
		if len(args) != 7 || (args[6] != "" && !operationFingerprintPattern.MatchString(args[6])) || !validSetupServerType(args[2]) || !imageTagInputPattern.MatchString(args[3]) || !instanceRuntimeVersionPattern.MatchString(args[4]) || (args[5] != "recommended" && args[5] != "latest" && args[5] != "pinned") {
			return errInstanceID
		}
		snapshot, _, _, err := b.snapshot(ctx)
		if err != nil {
			return err
		}
		records, err := b.records()
		if err != nil {
			return err
		}
		next := previous
		next.Runtime.Software = args[2]
		next.Runtime.ImageTag = args[3]
		next.Runtime.Version = args[4]
		next.Runtime.VersionPolicy = args[5]
		next.Runtime.ImageDigest = args[6]
		if previous.Runtime.Version != next.Runtime.Version || previous.Runtime.Software != next.Runtime.Software {
			channel, err := b.command(ctx, instanceRuntimeHelper, "channel", next.Runtime.Software, next.Runtime.Version, next.Runtime.VersionPolicy)
			if err != nil {
				return instanceRuntimeFailure("Selected release channel could not be verified.")
			}
			next.Runtime.PaperChannel = strings.TrimSpace(string(channel))
		}
		return b.applyVersion(ctx, previous, instanceReplaceRecord(records, next), snapshot, true)
	}
	return errInstanceID
}
