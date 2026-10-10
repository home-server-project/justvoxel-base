// Run with node. Exercises the browser's actual action logic without a DOM.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const source = fs.readFileSync(path.join(__dirname, "../webui/internal/server/static/storage-browser.js"), "utf8");
function functionSource(name, next) {
  return source.slice(source.indexOf("  function " + name + "("), source.indexOf(next, source.indexOf("  function " + name + "(")));
}
const actions = ["mount-for-now", "mount-permanently", "unmount-for-now", "make-permanent", "mount-now", "remove-permanent", "initialize_disk", "delete_partition", "format", "use-for-backups"];
const context = {
  actionButtons: actions.map(action => ({dataset: {storageAction: action}, hidden: true})),
  detailActions: {}, protectedNote: {}, detail: {}, migrateButton: {},
  // These bindings belong to initStorageBrowser's closure in actual browser execution.
  setupChoice: null, options: {},
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

// Backup adoption requires an actual partition and a current permanent mount.
for (const filesystem of ["xfs", "ext4", "btrfs"]) {
  const partition = {type: "part", filesystem, system: "No", readonly: "No"};
  for (const persistence of ["justvoxel", "external"]) {
    const status = {persistence, mounted: true, current_mount_point: "/var/mnt/vdb1", mount_point: "/var/mnt/vdb1"};
    check(partition, status, ["format", "delete_partition", "unmount-for-now", "use-for-backups", ...(persistence === "justvoxel" ? ["remove-permanent"] : [])]);
    check({...partition, system: "Yes"}, status, []);
    check({...partition, readonly: "Yes"}, status, []);
    check({...partition, type: "disk"}, status, ["unmount-for-now", ...(persistence === "justvoxel" ? ["remove-permanent"] : [])]);
  }
  check(partition, {persistence: "none", mounted: true}, ["format", "delete_partition", "unmount-for-now", "make-permanent"]);
  check(partition, {persistence: "none", mounted: false}, ["format", "delete_partition", "mount-for-now", "mount-permanently"]);
  check(partition, {persistence: "justvoxel", mounted: false, mount_point: "/var/mnt/vdb1"}, ["format", "delete_partition", "mount-now", "remove-permanent"]);
}
for (const filesystem of ["vfat", "exfat", "ntfs"]) {
  check({type: "part", filesystem}, {persistence: "justvoxel", mounted: true, current_mount_point: "/var/mnt/vdb1", mount_point: "/var/mnt/vdb1"}, ["format", "delete_partition", "unmount-for-now", "remove-permanent"]);
}
context.selectedAction = "use-for-backups";
const backupRequest = context.actionRequest();
assert.equal(backupRequest.family, "backup-partition");
assert.equal(backupRequest.operation, "");
assert.equal(backupRequest.mountPoint, "");

for (const filesystem of ["fat", "swap"]) {
  context.configurePartitionActions({type: "part", filesystem}, {persistence: "justvoxel", mounted: true, current_mount_point: "/var/mnt/vdb1", mount_point: "/var/mnt/vdb1"});
  assert.equal(context.actionButtons.find(button => button.dataset.storageAction === "use-for-backups").hidden, true);
}
context.configurePartitionActions({type: "part", filesystem: "xfs", mounted: "Yes"}, null);
assert.equal(context.actionButtons.find(button => button.dataset.storageAction === "use-for-backups").hidden, true);

// Setup shares the policy, with a separate choice limited to its eligible destinations.
context.setupChoice = {hidden: true};
context.options = {inlineDetails: true, onSelect() {}, canSelect: device => device === "/dev/vdb1"};
const setupPartition = {path: "/dev/vdb1", type: "part", filesystem: "xfs", minecraftCandidate: "Yes"};
const permanentMount = {persistence: "justvoxel", mounted: true, current_mount_point: "/var/mnt/vdb1", mount_point: "/var/mnt/vdb1"};
check(setupPartition, permanentMount, ["format", "delete_partition", "unmount-for-now", "remove-permanent"]);
assert.equal(context.setupChoice.hidden, false);
assert.equal(context.migrateButton.hidden, true);
for (const protection of [{system: "Yes"}, {readonly: "Yes"}]) {
  check({...setupPartition, ...protection, path: "/dev/protected"}, permanentMount, []);
  assert.equal(context.setupChoice.hidden, true);
  assert.equal(context.detailActions.hidden, true);
  assert.equal(context.protectedNote.hidden, false);
}
