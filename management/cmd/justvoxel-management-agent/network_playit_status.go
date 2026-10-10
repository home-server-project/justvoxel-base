package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"time"
)

type playitTunnel struct {
	DisplayAddress string `json:"display_address"`
	Destination    string `json:"destination"`
	Disabled       bool   `json:"is_disabled"`
}

type playitStatusResult struct {
	Summary   string         `json:"summary"`
	Connected bool           `json:"connected"`
	Tunnels   []playitTunnel `json:"tunnels"`
}

// Shared bounded, read-only protocol client; only tunnel presentation data leaves it.
var playitTunnelStatus = func(ctx context.Context) (playitStatusResult, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/libexec/justvoxel/remote-access-status", "playit", "--ipc-only", "--json")
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err != nil {
		return playitStatusResult{}, err
	}
	var result playitStatusResult
	err = json.Unmarshal(output, &result)
	return result, err
}
