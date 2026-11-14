package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

var networkCompatibilityInitMu sync.Mutex

// Capability results are presentation only. Publication never reads this cache.
type networkCompatibilityResult struct {
	Status string
	Reason string
}
type networkCompatibilityEntry struct {
	result  networkCompatibilityResult
	expires time.Time
	ready   chan struct{}
}
type networkCompatibilityCache struct {
	mu      sync.Mutex
	entries map[string]*networkCompatibilityEntry
}

func networkCompatibilityPublic(status, game string) networkCompatibilityResult {
	switch status {
	case "supported":
		return networkCompatibilityResult{Status: status}
	case "unsupported":
		return networkCompatibilityResult{Status: status, Reason: "Not supported by Minecraft Network"}
	case "compatibility-conflict":
		return networkCompatibilityResult{Status: "compatibility-unavailable", Reason: "Installed mods or Network compatibility files conflict. Review this server before joining Network."}
	case "compatibility-unavailable":
		return networkCompatibilityResult{Status: status, Reason: fmt.Sprintf("Network compatibility is not currently available for Minecraft %s.", game)}
	default:
		return networkCompatibilityResult{Status: "compatibility-check-unavailable", Reason: "Network compatibility could not be checked. Refresh to retry."}
	}
}

func (b *instanceRuntimeBackend) networkCompatibility(ctx context.Context, record instanceRuntimeRecord) networkCompatibilityResult {
	c := record.Runtime
	if !networkRecordEligible(c) {
		return networkCompatibilityPublic("unsupported", "")
	}
	if profile, _ := networkBackendProfile(c.Software); profile == networkPaperFamily {
		return networkCompatibilityPublic("supported", "")
	}
	unavailable := networkCompatibilityPublic("compatibility-check-unavailable", "")
	// Hash current relevant bytes before consulting the cache; a mod change or a
	// runtime/storage/evidence change cannot inherit another selection's result.
	keyContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	output, err := b.command(keyContext, minecraftNetworkHelper, "backend-availability-key", c.ID)
	cancel()
	fingerprint := strings.TrimSpace(string(output))
	if err != nil || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(fingerprint) {
		return unavailable
	}
	runtime, err := json.Marshal(record)
	if err != nil {
		return unavailable
	}
	key := c.ID + ":" + string(runtime) + ":" + fingerprint
	networkCompatibilityInitMu.Lock()
	if b.networkCompatibilityCache == nil {
		b.networkCompatibilityCache = &networkCompatibilityCache{}
	}
	cache := b.networkCompatibilityCache
	networkCompatibilityInitMu.Unlock()
	cache.mu.Lock()
	if cache.entries == nil {
		cache.entries = make(map[string]*networkCompatibilityEntry)
	}
	if entry := cache.entries[key]; entry != nil {
		if entry.ready != nil {
			ready := entry.ready
			cache.mu.Unlock()
			select {
			case <-ready:
				cache.mu.Lock()
				result := entry.result
				cache.mu.Unlock()
				return result
			case <-ctx.Done():
				return unavailable
			}
		}
		if time.Now().Before(entry.expires) {
			result := entry.result
			cache.mu.Unlock()
			return result
		}
		delete(cache.entries, key)
	}
	// Bound both retained entries and concurrent checks. Never evict an in-flight
	// entry and allow a second caller to resolve the same candidate concurrently.
	if len(cache.entries) >= 128 {
		for oldKey, entry := range cache.entries {
			if entry.ready == nil {
				delete(cache.entries, oldKey)
				break
			}
		}
	}
	if len(cache.entries) >= 128 {
		cache.mu.Unlock()
		return unavailable
	}
	entry := &networkCompatibilityEntry{ready: make(chan struct{})}
	cache.entries[key] = entry
	cache.mu.Unlock()
	checkContext, cancel := context.WithTimeout(ctx, 155*time.Second)
	output, err = b.command(checkContext, minecraftNetworkHelper, "backend-availability", c.ID)
	cancel()
	result := unavailable
	var response struct {
		Status string `json:"status"`
	}
	if err == nil && len(output) <= 256 && instanceDecode(output, &response) == nil {
		result = networkCompatibilityPublic(response.Status, c.Version)
	}
	ttl := 5 * time.Minute
	if result.Status == "compatibility-check-unavailable" {
		ttl = 20 * time.Second
	}
	cache.mu.Lock()
	entry.result, entry.expires = result, time.Now().Add(ttl)
	close(entry.ready)
	entry.ready = nil
	cache.mu.Unlock()
	return result
}
