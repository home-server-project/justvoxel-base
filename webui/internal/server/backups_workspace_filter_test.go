package server

import (
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func TestBackupsWorkspaceServerFilterMarkupPreservesArchiveReferences(t *testing.T) {
	client := &fakeNewBackupsAPI{backups: api.AdminRestoreBackupsResponse{
		Backups: []api.AdminRestoreBackup{
			{ID: "jv-abc123/minecraft-paper-2026-10-09-1506.tar.gz", SizeBytes: 2048, CreatedAt: "2026-10-09T15:06:00Z", MetadataStatus: "missing"},
			{ID: "jv-def456/minecraft-purpur-2026-10-09-1506.tar.gz", SizeBytes: 3072, CreatedAt: "2026-10-09T15:06:00Z", MetadataStatus: "missing"},
			{ID: "minecraft-2026-09-15-043000.tar.gz", SizeBytes: 1024, CreatedAt: "2026-09-15T04:30:00Z", MetadataStatus: "missing"},
		},
	}}
	response := backupsSafetyRequest(t, client, http.MethodGet, "/workspace/backups", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("Backups workspace returned %d: %s", response.Code, response.Body.String())
	}
	page := response.Body.String()
	for _, want := range []string{
		`data-backup-server-filter`,
		`<option value="all">All servers</option>`,
		`<option value="legacy">Older / Unassigned</option>`,
		`data-backup-size-bytes="2048"`,
		`value="jv-abc123/minecraft-paper-2026-10-09-1506.tar.gz"`,
		`value="jv-def456/minecraft-purpur-2026-10-09-1506.tar.gz"`,
		`value="minecraft-2026-09-15-043000.tar.gz"`,
		`data-backup-visible-count`,
		`data-backup-visible-size`,
		`data-backup-filter-empty`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("backup filter markup missing %q", want)
		}
	}
	if strings.Contains(page, `/instances/jv-`) {
		t.Fatal("filter incorrectly introduces a new instances/ backup directory")
	}
}

func TestBackupsWorkspaceServerFilterBehavior(t *testing.T) {
	source, err := assets.ReadFile("static/new-backups.js")
	if err != nil {
		t.Fatal(err)
	}
	program := "const source = " + strconv.Quote(string(source)) + ";\n" + `
const assert = require("node:assert/strict");
global.window = {};
global.document = {
  querySelector() { return null; },
  createElement() { return { value: "", textContent: "" }; },
};
eval(source);

const ids = [
  "jv-def456/minecraft-purpur-2026-10-09-1506.tar.gz",
  "minecraft-2026-09-15-043000.tar.gz",
  "jv-abc123/minecraft-paper-2026-10-09-1506.tar.gz",
];
const sizes = ["3072", "1024", "2048"];
const cards = ids.map((id, i) => ({
  dataset: { backupSizeBytes: sizes[i] },
  hidden: false,
  querySelector() { return boxes[i]; },
}));
const boxes = ids.map((id, i) => ({
  value: id,
  checked: false,
  closest() { return cards[i]; },
  addEventListener(_, fn) { this.changed = fn; },
}));
const inserted = [];
const legacy = { value: "legacy" };
const filter = {
  value: "all",
  querySelector() { return legacy; },
  insertBefore(option) { inserted.push(option); },
  addEventListener(_, fn) { this.changed = fn; },
};
const summaryCount = { textContent: "3" };
const summarySize = { textContent: "6.0 KiB" };
const selectedCount = { textContent: "0" };
const empty = { hidden: true };
const restore = { disabled: true, dataset: { restoreBusy: "0" }, addEventListener() {} };
const remove = { disabled: true, addEventListener() {} };
const elements = {
  "[data-backup-server-filter]": filter,
  "[data-backup-visible-count]": summaryCount,
  "[data-backup-visible-size]": summarySize,
  "[data-backup-filter-empty]": empty,
  "[data-backup-selected-count]": selectedCount,
  "[data-backup-delete-selected]": remove,
  "[data-backup-restore-selected]": restore,
};
const root = {
  dataset: {},
  querySelector(selector) { return elements[selector] || null; },
  querySelectorAll(selector) {
    if (selector === "[data-backup-select]") return boxes;
    if (selector === "[data-backup-card]") return cards;
    return [];
  },
};
window.JustVoxelNewBackups.init(root);
assert.deepEqual(inserted.map(o => o.value), ["jv-abc123", "jv-def456"], "all instance folders, including former servers, must be available");
boxes[0].checked = true;
boxes[0].changed();
assert.equal(selectedCount.textContent, "1");
assert.equal(restore.disabled, false);

filter.value = "jv-abc123";
filter.changed();
assert.deepEqual(cards.map(c => c.hidden), [true, true, false]);
assert.deepEqual(boxes.map(b => b.checked), [false, false, false], "switching must clear selections");
assert.equal(selectedCount.textContent, "0");
assert.equal(remove.disabled, true);
assert.equal(restore.disabled, true);
assert.equal(summaryCount.textContent, "1");
assert.equal(summarySize.textContent, "2.0 KiB");
assert.equal(empty.hidden, true);

filter.value = "legacy";
filter.changed();
assert.deepEqual(cards.map(c => c.hidden), [true, false, true]);
assert.equal(summaryCount.textContent, "1");
assert.equal(summarySize.textContent, "1.0 KiB");

filter.value = "all";
filter.changed();
assert.deepEqual(cards.map(c => c.hidden), [false, false, false]);
assert.equal(summaryCount.textContent, "3");
assert.equal(summarySize.textContent, "6.0 KiB");
`
	command := exec.Command("node", "-e", program)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("backup filter interaction test: %v\n%s", err, output)
	}
}
