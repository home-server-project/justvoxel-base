// Run with node. Exercises the browser's actual action logic without a DOM.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const source = fs.readFileSync(path.join(__dirname, "../webui/internal/server/static/storage-browser.js"), "utf8");
function functionSource(name, next) {
  return source.slice(source.indexOf("  function " + name + "("), source.indexOf(next, source.indexOf("  function " + name + "(")));
}
const actions = ["mount-for-now", "mount-permanently", "unmount-for-now", "make-permanent", "mount-now", "remove-permanent", "initialize_disk", "delete_partition", "format"];
const context = {
  actionButtons: actions.map(action => ({dataset: {storageAction: action}, hidden: true})),
  detailActions: {}, protectedNote: {}, detail: {}, migrateButton: {},
  normalizedFilesystem: data => data.filesystem || "",
  setOptional: () => {}, closeActionMenu: () => {},
  selectedAction: "mount-permanently", mountInput: {value: "/var/mnt/sda"},
};
vm.createContext(context);
vm.runInContext(functionSource("showStorageAction", "  async function loadMountStatus") + functionSource("actionRequest", "  function resetActionDialog"), context);
const usb = {type: "disk", filesystem: "vfat", transport: "usb", canInitialize: "No", minecraftCandidate: "Yes", mounted: "No"};
function check(data, status, expected) {
  context.configurePartitionActions(data, status);
  assert.deepEqual(context.actionButtons.filter(button => !button.hidden).map(button => button.dataset.storageAction).sort(), expected.sort());
}
check(usb, {persistence: "none", mounted: false}, ["mount-for-now", "mount-permanently"]);
assert.equal(context.migrateButton.hidden, true);
check(usb, {persistence: "none", mounted: true}, ["unmount-for-now", "make-permanent"]);
check(usb, {persistence: "justvoxel", mounted: true}, ["unmount-for-now", "remove-permanent"]);
check(usb, {persistence: "justvoxel", mounted: false, mount_point: "/var/mnt/sda"}, ["mount-now", "remove-permanent"]);
check({...usb, system: "Yes"}, {persistence: "none"}, []);
check({...usb, readonly: "Yes"}, {persistence: "none"}, []);
for (const filesystem of ["xfs", "ext4", "btrfs", "ntfs", "vfat", "exfat"]) {
  check({...usb, filesystem}, {persistence: "none", mounted: false}, ["mount-for-now", "mount-permanently"]);
}
check({type: "disk", canInitialize: "Yes", filesystem: ""}, null, ["initialize_disk"]);
check({type: "disk", canInitialize: "No", partitionTable: "gpt"}, null, []);
check({type: "disk", canInitialize: "No", partitionTable: "msdos"}, null, []);
const request = context.actionRequest();
assert.equal(request.family, "mounts");
assert.equal(request.operation, "persist");
assert.equal(request.mountPoint, "/var/mnt/sda");
