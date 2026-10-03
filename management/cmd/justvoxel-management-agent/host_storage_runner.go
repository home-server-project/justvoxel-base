package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// This boundary also lets tests inspect argv and stdin without a system manager.
var runHostStorageCommand = func(ctx context.Context, executable string, args []string, request []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	if len(request) > 0 {
		cmd.Stdin = bytes.NewReader(request)
	}
	// A surviving service can hold the output pipes after systemd-run is killed.
	// Bound that wait so we can reach the explicit unit cleanup below.
	cmd.WaitDelay = time.Second
	return cmd.CombinedOutput()
}

var runHostStorageMutation = hostStorageMutation

func hostStorageMutation(ctx context.Context, helper, action string, request []byte) ([]byte, error) {
	if action != "apply" {
		return nil, errors.New("host storage runner only accepts apply")
	}
	switch helper {
	case adminStorageProvisionHelper, adminStorageMountsHelper, adminStorageActionsHelper:
	default:
		return nil, errors.New("unknown host storage helper")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, fmt.Errorf("generate host storage unit: %w", err)
	}
	unit := "justvoxel-host-storage-" + hex.EncodeToString(id[:]) + ".service"
	// Bound service termination as well as the cleanup client timeout.
	output, err := runHostStorageCommand(ctx, "/usr/bin/systemd-run", []string{
		"--quiet", "--pipe", "--wait", "--collect", "--property=Type=exec",
		"--property=TimeoutStopSec=5s",
		"--unit=" + unit, "--", helper, action,
	}, request)
	if err != nil || ctx.Err() != nil {
		// Killing the client does not stop its service. Use an independent,
		// bounded context and wait for systemd to stop this internally named unit.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, cleanupErr := runHostStorageCommand(cleanupCtx, "/usr/bin/systemctl", []string{"stop", "--", unit}, nil)
		if cleanupErr != nil {
			cleanupErr = fmt.Errorf("stop host storage unit: %w", cleanupErr)
		}
		return output, errors.Join(err, ctx.Err(), cleanupErr)
	}
	return output, nil
}
