package server

import (
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestBackupsWorkspaceSelectedServerPreservesArchiveReferences(t *testing.T) {
	app, client := phase7App(t)
	response := phase7Request(app, http.MethodGet, "/workspace/backups?server=jv-family1", "")
	if response.Code != http.StatusOK {
		t.Fatalf("Backups workspace returned %d: %s", response.Code, response.Body.String())
	}
	page := response.Body.String()
	for _, want := range []string{
		`data-selected-server-id="jv-family1"`,
		`data-backup-size-bytes="1"`,
		`value="Paper1-backup-paper-2026-10-28-0831.tar.gz"`,
		`data-backup-restore-id="Paper1-backup-paper-2026-10-28-0831.tar.gz"`,
		`data-backup-delete-ref="jv-family1/Paper1-backup-paper-2026-10-28-0831.tar.gz"`,
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
	if strings.Contains(page, `data-backup-server-filter`) {
		t.Fatal("selected-instance Backups exposed the global server filter")
	}
	if !strings.Contains(strings.Join(client.backupCalls, "|"), "jv-family1:inventory") {
		t.Fatal("selected server inventory was not requested", client.backupCalls)
	}
	for _, call := range client.backupCalls {
		if !strings.HasPrefix(call, "jv-family1:") {
			t.Fatal("Backups requested another server", call)
		}
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
  dataset: { backupDeleteRef: id },
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
