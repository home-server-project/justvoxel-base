package server

import (
	"regexp"
	"strings"
	"testing"
)

func TestSystemMonitorProcessCountUIContract(t *testing.T) {
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	selectMarkup := regexp.MustCompile(`(?s)<select data-monitor-process-count>(.*?)</select>`).FindStringSubmatch(string(header))
	if len(selectMarkup) != 2 {
		t.Fatal("top processes select missing")
	}
	monitorHeader := string(header)
	gear := strings.Index(monitorHeader, `data-system-monitor-profile-toggle`)
	close := strings.Index(monitorHeader, `data-system-monitor-close`)
	if gear < 0 || close <= gear || strings.Contains(monitorHeader, `data-system-monitor-refresh`) {
		t.Error("System Monitor header controls must be Settings and Close")
	}
	settings := regexp.MustCompile(`(?s)<button[^>]*data-system-monitor-profile-toggle[^>]*>(.*?)</button>`).FindStringSubmatch(monitorHeader)
	if len(settings) != 2 || !strings.Contains(settings[1], `<svg `) || !strings.Contains(settings[1], `aria-hidden="true"`) || !strings.Contains(settings[1], `viewBox="0 0 24 24"`) || strings.Contains(settings[1], "⚙") {
		t.Error("System Monitor settings must use an accessible centered SVG gear")
	}
	options := regexp.MustCompile(`<option value="([0-9]+)">([0-9]+)</option>`).FindAllStringSubmatch(selectMarkup[1], -1)
	if len(options) != 3 || options[0][1] != "5" || options[0][2] != "5" || options[1][1] != "10" || options[1][2] != "10" || options[2][1] != "15" || options[2][2] != "15" || strings.Count(selectMarkup[1], "<option") != 3 {
		t.Fatalf("top processes choices = %v, want exactly 5, 10, 15", options)
	}

	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(script)
	for _, want := range []string{
		`refreshTimer = window.setInterval(refreshMonitor, 2000);`,
		`const processLimit = [5, 10, 15].includes(Number(profile.process_count)) ? Number(profile.process_count) : 10;`,
		`if (processesCard) processesCard.dataset.processCount = String(processLimit);`,
		`processes.slice(0, processLimit)`,
		`if (lastMonitorData) renderMonitor(lastMonitorData);`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("System Monitor behavior missing %q", want)
		}
	}
	if strings.Contains(source, `data-system-monitor-refresh`) || strings.Contains(source, `refreshButton.addEventListener("click", refreshMonitor)`) {
		t.Error("System Monitor must not retain a manual refresh handler")
	}
	if !strings.Contains(source, `if (alertsCard) alertsCard.hidden = profile.alerts === false || alerts.length === 0;`) {
		t.Error("empty Alerts card must remain hidden")
	}

	css, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), `.system-monitor-card[data-monitor-card="processes"] .system-monitor-table{max-height:none}`) || strings.Contains(string(css), `.system-monitor-card[data-monitor-card="processes"] .system-monitor-table{max-height:130px}`) {
		t.Error("Processes table must grow with the selected process rows")
	}
}
