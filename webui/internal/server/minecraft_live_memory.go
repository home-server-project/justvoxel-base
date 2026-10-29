package server

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"regexp"
	"time"
)

const minecraftContainersGlancesURL = "http://127.0.0.1:61208/api/4/containers"

var minecraftContainerName = regexp.MustCompile(`^minecraft-(jv-[a-z0-9]{6,12})$`)

type minecraftLiveMemory struct {
	ID         string   `json:"id"`
	State      string   `json:"state,omitempty"`
	CardHealth string   `json:"card_health,omitempty"`
	StateClass string   `json:"state_class,omitempty"`
	Running    *bool    `json:"running,omitempty"`
	CanSwitch  *bool    `json:"can_switch,omitempty"`
	UsageBytes *float64 `json:"usage_bytes,omitempty"`
	LimitBytes *float64 `json:"limit_bytes,omitempty"`
}

// Decode individual fields so one malformed container cannot poison other rows.
// Null, strings, negative and non-finite values are never memory measurements.
func glancesMemoryNumber(raw json.RawMessage) (float64, bool) {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return 0, false
	}
	number, ok := value.(float64)
	return number, ok && number >= 0 && !math.IsNaN(number) && !math.IsInf(number, 0)
}

func (a *App) minecraftServersLiveMemory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	session, _, ok := a.systemMonitorIdentity(w, r)
	if !ok {
		return
	}
	result := make([]minecraftLiveMemory, 0)
	byID := make(map[string]int)
	instances, err := a.independentServerInventory(r.Context(), session)
	if err == nil {
		lifecycle, supported := a.api.(minecraftServerLifecycleAPI)
		for _, instance := range instances {
			row := minecraftLiveMemory{ID: instance.ID}
			if supported {
				status, err := lifecycle.MinecraftInstanceStatus(r.Context(), session, instance.ID)
				if err == nil && status.ID == instance.ID {
					var summary minecraftServerSummary
					summary.setState(status.State)
					row.State, row.StateClass, row.CardHealth = summary.State, summary.StateClass, summary.CardHealth
					row.Running, row.CanSwitch = &summary.Running, &summary.CanSwitch
				}
			}
			byID[row.ID] = len(result)
			result = append(result, row)
		}
	}
	// RAM is optional enrichment. Preserve memory-only rows if inventory or
	// lifecycle is unavailable, without inferring lifecycle state from Glances.
	for _, memory := range a.minecraftLiveMemoryRows(r) {
		if index, ok := byID[memory.ID]; ok {
			result[index].UsageBytes, result[index].LimitBytes = memory.UsageBytes, memory.LimitBytes
		} else {
			byID[memory.ID] = len(result)
			result = append(result, memory)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (a *App) minecraftLiveMemoryRows(r *http.Request) []minecraftLiveMemory {
	url := a.minecraftContainersURL
	if url == "" {
		url = minecraftContainersGlancesURL
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	request.Header.Set("Accept", "application/json")
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil
	}
	const maxBody = 8 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	var rows []json.RawMessage
	if err != nil || len(body) > maxBody || json.Unmarshal(body, &rows) != nil || rows == nil {
		return nil
	}
	result := make([]minecraftLiveMemory, 0)
	for _, raw := range rows {
		var row map[string]json.RawMessage
		if json.Unmarshal(raw, &row) != nil {
			continue
		}
		var name string
		if json.Unmarshal(row["name"], &name) != nil {
			continue
		}
		match := minecraftContainerName.FindStringSubmatch(name)
		if match == nil {
			continue
		}
		usage, ok := glancesMemoryNumber(row["memory_usage"])
		if !ok {
			continue
		}
		if inactive, ok := glancesMemoryNumber(row["memory_inactive_file"]); ok {
			usage = math.Max(0, usage-inactive)
		}
		memory := minecraftLiveMemory{ID: match[1], UsageBytes: &usage}
		if limit, ok := glancesMemoryNumber(row["memory_limit"]); ok && limit > 0 {
			memory.LimitBytes = &limit
		}
		result = append(result, memory)
	}
	return result
}
