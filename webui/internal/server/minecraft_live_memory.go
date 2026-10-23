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
	UsageBytes float64  `json:"usage_bytes"`
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
	if _, _, ok := a.systemMonitorIdentity(w, r); !ok {
		return
	}
	url := a.minecraftContainersURL
	if url == "" {
		url = minecraftContainersGlancesURL
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		http.Error(w, "live RAM unavailable", http.StatusBadGateway)
		return
	}
	request.Header.Set("Accept", "application/json")
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		http.Error(w, "live RAM unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		http.Error(w, "live RAM unavailable", http.StatusBadGateway)
		return
	}
	const maxBody = 8 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	var rows []map[string]json.RawMessage
	if err != nil || len(body) > maxBody || json.Unmarshal(body, &rows) != nil || rows == nil {
		http.Error(w, "invalid live RAM response", http.StatusBadGateway)
		return
	}
	result := make([]minecraftLiveMemory, 0)
	for _, row := range rows {
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
		memory := minecraftLiveMemory{ID: match[1], UsageBytes: usage}
		if limit, ok := glancesMemoryNumber(row["memory_limit"]); ok && limit > 0 {
			memory.LimitBytes = &limit
		}
		result = append(result, memory)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
