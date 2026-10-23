package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	errInstanceName          = errors.New("Use at most 64 characters: letters, numbers, spaces, or . _ - ' ( ).")
	errInstanceNameDuplicate = errors.New("Another registered instance already uses that display name.")
	errInstanceNotFound      = errors.New("Instance is not registered.")
	errInstanceID            = errors.New("Invalid Instance ID.")
)

type minecraftRegisteredInstance struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// Names are optional presentation metadata. Identity remains in the original
// ID-only records, and runtime/backup code never reads this directory.
// Retained metadata for absent IDs is historical evidence, not an active server.
type minecraftInstanceNameRecord struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Software    string `json:"software"`
}

func validateInstanceName(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", errInstanceName
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", errInstanceName
		}
	}
	name = strings.TrimSpace(name)
	for _, r := range name {
		if !(unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) || r == ' ' || strings.ContainsRune("._-'()", r)) {
			return "", errInstanceName
		}
	}
	if utf8.RuneCountInString(name) > 64 || name == "." || name == ".." {
		return "", errInstanceName
	}
	return name, nil
}

func instanceDefaultName(software string) string {
	if c, ok := javaServerSoftware(software); ok {
		return "JV " + c.Name
	}
	return "JV Minecraft"
}

func validInstanceSoftware(software string) bool {
	return software == "" || validSetupServerType(software)
}

func instanceNameUsed(instances []minecraftRegisteredInstance, id, name string) bool {
	for _, instance := range instances {
		if instance.ID != id && strings.EqualFold(instance.DisplayName, name) {
			return true
		}
	}
	return false
}

func generatedInstanceName(instances []minecraftRegisteredInstance, id, software string) string {
	base := instanceDefaultName(software)
	name := base
	for suffix := 2; instanceNameUsed(instances, id, name); suffix++ {
		name = fmt.Sprintf("%s %d", base, suffix)
	}
	return name
}

// Only the empty directory left by the wrong-service-group release is eligible.
// Keep this policy separate so ownership decisions can be tested without root.
func (s *minecraftInstanceStore) namesGroupRepairable(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.IsDir() && int(stat.Uid) == s.uid && int(stat.Gid) != s.gid &&
		info.Mode().Perm() == 0700 && info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0
}

func (s *minecraftInstanceStore) openNames(create bool) (*os.File, error) {
	return s.openNamesDirectory(create, false)
}

// repair is used only by startup reconciliation, under the identity-directory lock.
func (s *minecraftInstanceStore) openNamesDirectory(create, repair bool) (*os.File, error) {
	path := filepath.Join(s.directory, "names")
	created := false
	if create {
		err := os.Mkdir(path, 0700)
		if err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		created = err == nil
	}
	dir, err := os.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*os.File, error) {
		dir.Close()
		return nil, err
	}
	info, err := dir.Stat()
	if err != nil {
		return fail(err)
	}
	if !created && repair && !s.private(info, 0700) {
		if err := s.repairNamesGroup(dir, info); err != nil {
			return fail(err)
		}
		info, err = dir.Stat()
		if err != nil {
			return fail(err)
		}
	}
	if created {
		if err := dir.Chown(s.uid, s.gid); err != nil {
			return fail(err)
		}
		if err := dir.Sync(); err != nil {
			return fail(err)
		}
		info, err = dir.Stat()
		if err != nil {
			return fail(err)
		}
	}
	if !s.private(info, 0700) {
		return fail(errors.New("instance name directory must be private and owned by root"))
	}
	return dir, nil
}

// info comes from the no-follow directory FD. Recheck the normal contract after
// repair; eligibility alone never makes malformed metadata acceptable.
func (s *minecraftInstanceStore) repairNamesGroup(dir *os.File, info os.FileInfo) error {
	if !s.namesGroupRepairable(info) {
		return errors.New("instance name directory is not eligible for group repair")
	}
	// Open a separate stream from the trusted FD so prior reads cannot hide entries.
	// Even hidden files and unpublished temporary records disqualify repair.
	fd, err := syscall.Openat(int(dir.Fd()), ".", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	check := os.NewFile(uintptr(fd), dir.Name())
	entries, err := check.ReadDir(1)
	closeErr := check.Close()
	if len(entries) != 0 || err != io.EOF {
		return errors.New("instance name directory is not verified empty")
	}
	if closeErr != nil {
		return closeErr
	}
	if err := dir.Chown(s.uid, s.gid); err != nil {
		return err
	}
	if err := dir.Sync(); err != nil {
		return err
	}
	info, err = dir.Stat()
	if err != nil {
		return err
	}
	if !s.private(info, 0700) {
		return errors.New("instance name directory must be private and owned by root")
	}
	return nil
}

// Reconcile existing optional metadata before serving inventory. Never create it.
// Unsafe state remains untouched and continues to fail normal registry validation.
func (s *minecraftInstanceStore) reconcileNamesAtStartup() error {
	dir, err := s.openDirectory(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := syscall.Flock(int(dir.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(dir.Fd()), syscall.LOCK_UN)
	names, err := s.openNamesDirectory(false, true)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return names.Close()
}

func (s *minecraftInstanceStore) readNames() (map[string]minecraftInstanceNameRecord, error) {
	records := make(map[string]minecraftInstanceNameRecord)
	dir, err := s.openNames(false)
	if errors.Is(err, os.ErrNotExist) {
		return records, nil
	}
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".name-") {
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() || !s.private(info, 0600) {
				return nil, errors.New("invalid instance name temporary file")
			}
			continue // Unpublished atomic-write temporary files.
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if entry.Name() != id+".json" || !instanceIDPattern.MatchString(id) {
			return nil, errors.New("invalid instance name record filename")
		}
		fd, err := syscall.Openat(int(dir.Fd()), entry.Name(), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, err
		}
		file := os.NewFile(uintptr(fd), entry.Name())
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() || !s.private(info, 0600) {
			file.Close()
			return nil, errors.New("invalid instance name record permissions")
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 4097))
		file.Close()
		if readErr != nil {
			return nil, readErr
		}
		if len(data) > 4096 {
			return nil, errors.New("invalid instance name record")
		}
		var record minecraftInstanceNameRecord
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&record); err != nil {
			return nil, err
		}
		var extra any
		name, nameErr := validateInstanceName(record.DisplayName)
		if decoder.Decode(&extra) != io.EOF || record.ID != id || nameErr != nil || name == "" || name != record.DisplayName || !validInstanceSoftware(record.Software) {
			return nil, errors.New("invalid instance name record")
		}
		records[id] = record
	}
	return records, nil
}

// Caller holds the existing identity directory lock. Missing metadata is not
// materialized by reads; stored names take priority over generated fallback names.
func (s *minecraftInstanceStore) registeredInstances(legacySoftware string) ([]minecraftRegisteredInstance, map[string]minecraftInstanceNameRecord, error) {
	entries, err := os.ReadDir(s.directory)
	if err != nil {
		return nil, nil, err
	}
	names, err := s.readNames()
	if err != nil {
		return nil, nil, err
	}
	instances := []minecraftRegisteredInstance{}
	seen := make(map[string]bool)
	legacyID := ""
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id, err := s.readRecord(entry.Name())
		if err != nil {
			return nil, nil, err
		}
		if strings.HasPrefix(entry.Name(), "jv-") && entry.Name() != id+".json" {
			return nil, nil, errors.New("instance record filename does not match ID")
		}
		if seen[id] {
			return nil, nil, errInstanceDuplicate
		}
		seen[id] = true
		if entry.Name() == "minecraft.json" {
			legacyID = id
		}
		instance := minecraftRegisteredInstance{ID: id}
		if record, ok := names[id]; ok {
			instance.DisplayName = record.DisplayName
			if instanceNameUsed(instances, id, instance.DisplayName) {
				return nil, nil, errInstanceNameDuplicate
			}
		}
		instances = append(instances, instance)
	}
	sort.Slice(instances, func(i, j int) bool { return instances[i].ID < instances[j].ID })
	for i := range instances {
		if instances[i].DisplayName == "" {
			software := ""
			if instances[i].ID == legacyID {
				software = legacySoftware
			}
			instances[i].DisplayName = generatedInstanceName(instances, instances[i].ID, software)
		}
	}
	return instances, names, nil
}

func (s *minecraftInstanceStore) listInstances(legacySoftware string) ([]minecraftRegisteredInstance, error) {
	dir, err := s.openDirectory(false)
	if errors.Is(err, os.ErrNotExist) {
		return []minecraftRegisteredInstance{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	if err := syscall.Flock(int(dir.Fd()), syscall.LOCK_SH); err != nil {
		return nil, err
	}
	defer syscall.Flock(int(dir.Fd()), syscall.LOCK_UN)
	instances, _, err := s.registeredInstances(legacySoftware)
	return instances, err
}

// Each record is synced before atomic publication; the directory is synced too.
// replace is only for presentation metadata, never immutable identity records.
func (s *minecraftInstanceStore) publishRegistryRecord(dir *os.File, filename, prefix string, value any, replace bool) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir.Name(), prefix)
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := temp.Chown(s.uid, s.gid); err != nil {
		return err
	}
	if err := temp.Chmod(0600); err != nil {
		return err
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	path := filepath.Join(dir.Name(), filename)
	if replace {
		err = os.Rename(temp.Name(), path)
	} else {
		err = os.Link(temp.Name(), path)
	}
	if err != nil {
		return err
	}
	return dir.Sync()
}

// Internal foundation only: no creation/lifecycle route, container, or unit.
// Name metadata is published first. A failed/interrupted identity publication
// can leave retained metadata, but never a registered identity with a lost name.
// Retained name records reserve IDs for future logical registration. Callers
// supply the selected legacy software so unnamed legacy defaults are reserved too.
func (s *minecraftInstanceStore) registerLogicalInstance(suffix string, manual bool, software, name, legacySoftware string) (minecraftRegisteredInstance, error) {
	if manual && !instanceSuffixPattern.MatchString(suffix) {
		return minecraftRegisteredInstance{}, errInstanceSuffix
	}
	name, err := validateInstanceName(name)
	if err != nil {
		return minecraftRegisteredInstance{}, err
	}
	if !validInstanceSoftware(software) || software == "" || !validInstanceSoftware(legacySoftware) {
		return minecraftRegisteredInstance{}, errors.New("invalid Minecraft software")
	}
	dir, err := s.openDirectory(true)
	if err != nil {
		return minecraftRegisteredInstance{}, err
	}
	defer dir.Close()
	if err := syscall.Flock(int(dir.Fd()), syscall.LOCK_EX); err != nil {
		return minecraftRegisteredInstance{}, err
	}
	defer syscall.Flock(int(dir.Fd()), syscall.LOCK_UN)
	instances, names, err := s.registeredInstances(legacySoftware)
	if err != nil {
		return minecraftRegisteredInstance{}, err
	}
	id := "jv-" + strings.ToLower(suffix)
	if !manual {
		entropy := make([]byte, 16)
		if _, err := io.ReadFull(s.random, entropy); err != nil {
			return minecraftRegisteredInstance{}, err
		}
		space := new(big.Int).Exp(big.NewInt(36), big.NewInt(12), nil)
		value := new(big.Int).Mod(new(big.Int).SetBytes(entropy), space).Text(36)
		id = "jv-" + strings.Repeat("0", 12-len(value)) + value
	}
	for _, instance := range instances {
		if instance.ID == id {
			return minecraftRegisteredInstance{}, errInstanceDuplicate
		}
	}
	if _, exists := names[id]; exists {
		return minecraftRegisteredInstance{}, errInstanceDuplicate
	}
	if name == "" {
		name = generatedInstanceName(instances, id, software)
	}
	if instanceNameUsed(instances, id, name) {
		return minecraftRegisteredInstance{}, errInstanceNameDuplicate
	}
	namesDir, err := s.openNames(true)
	if err != nil {
		return minecraftRegisteredInstance{}, err
	}
	defer namesDir.Close()
	// Persist the names-directory entry before publishing records within it.
	if err := dir.Sync(); err != nil {
		return minecraftRegisteredInstance{}, err
	}
	if err := s.publishRegistryRecord(namesDir, id+".json", ".name-*", minecraftInstanceNameRecord{ID: id, DisplayName: name, Software: software}, false); err != nil {
		return minecraftRegisteredInstance{}, err
	}
	if err := s.publishRegistryRecord(dir, id+".json", ".instance-*", minecraftInstanceRecord{ID: id}, false); err != nil {
		return minecraftRegisteredInstance{}, err
	}
	return minecraftRegisteredInstance{ID: id, DisplayName: name}, nil
}

func (s *minecraftInstanceStore) renameInstance(id, name, legacySoftware string) (minecraftRegisteredInstance, error) {
	if !instanceIDPattern.MatchString(id) {
		return minecraftRegisteredInstance{}, errInstanceID
	}
	name, err := validateInstanceName(name)
	if err != nil {
		return minecraftRegisteredInstance{}, err
	}
	dir, err := s.openDirectory(false)
	if errors.Is(err, os.ErrNotExist) {
		return minecraftRegisteredInstance{}, errInstanceNotFound
	}
	if err != nil {
		return minecraftRegisteredInstance{}, err
	}
	defer dir.Close()
	if err := syscall.Flock(int(dir.Fd()), syscall.LOCK_EX); err != nil {
		return minecraftRegisteredInstance{}, err
	}
	defer syscall.Flock(int(dir.Fd()), syscall.LOCK_UN)
	// Deletion holds this same registry lock through name confirmation and
	// retirement. After interruption, its durable intent prevents a rename from
	// invalidating the only supported typed-name retry.
	if _, e := instanceRuntimes.readPrivate(instanceRuntimes.deletePath(id)); !errors.Is(e, os.ErrNotExist) {
		return minecraftRegisteredInstance{}, errInstanceRecovery
	}
	instances, names, err := s.registeredInstances(legacySoftware)
	if err != nil {
		return minecraftRegisteredInstance{}, err
	}
	found := false
	for _, instance := range instances {
		if instance.ID == id {
			found = true
		}
	}
	if !found {
		return minecraftRegisteredInstance{}, errInstanceNotFound
	}
	software := names[id].Software
	legacy, err := s.readRecord("minecraft.json")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return minecraftRegisteredInstance{}, err
	}
	if legacy == id && legacySoftware != "" && validInstanceSoftware(legacySoftware) {
		software = legacySoftware
	}
	if name == "" {
		name = generatedInstanceName(instances, id, software)
	}
	if instanceNameUsed(instances, id, name) {
		return minecraftRegisteredInstance{}, errInstanceNameDuplicate
	}
	// A newly explicit name must not displace another unnamed instance's fallback.
	namesDir, err := s.openNames(true)
	if err != nil {
		return minecraftRegisteredInstance{}, err
	}
	defer namesDir.Close()
	if err := dir.Sync(); err != nil {
		return minecraftRegisteredInstance{}, err
	}
	if err := s.publishRegistryRecord(namesDir, id+".json", ".name-*", minecraftInstanceNameRecord{ID: id, DisplayName: name, Software: software}, true); err != nil {
		return minecraftRegisteredInstance{}, err
	}
	return minecraftRegisteredInstance{ID: id, DisplayName: name}, nil
}

// Read the selected software through existing read-only discovery, never return
// configuration fields. Unconfigured/unavailable appliances use a neutral name.
func minecraftLegacyNameSoftware(r *http.Request) string {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	output, err := runAdminDiscoveryHelper(ctx, "configuration")
	if err != nil {
		return ""
	}
	var configuration adminConfigurationDiscovery
	if json.Unmarshal(output, &configuration) != nil || !configuration.Configured || !validInstanceSoftware(configuration.Minecraft.ServerType) {
		return ""
	}
	return configuration.Minecraft.ServerType
}

func (s *server) minecraftInstancesList(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireReadAccess(w, r); !ok {
		return
	}
	instances, err := minecraftInstances.listInstances(minecraftLegacyNameSoftware(r))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Instance registry is unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Instances []minecraftRegisteredInstance `json:"instances"`
	}{Instances: instances})
}

func (s *server) minecraftInstanceRename(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	id := r.PathValue("id")
	if !instanceIDPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, errInstanceID.Error())
		return
	}
	var request struct {
		DisplayName string `json:"display_name"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if _, err := validateInstanceName(request.DisplayName); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	instance, err := minecraftInstances.renameInstance(id, request.DisplayName, minecraftLegacyNameSoftware(r))
	if err != nil {
		status := http.StatusServiceUnavailable
		switch {
		case errors.Is(err, errInstanceID), errors.Is(err, errInstanceName):
			status = http.StatusBadRequest
		case errors.Is(err, errInstanceNameDuplicate):
			status = http.StatusConflict
		case errors.Is(err, errInstanceNotFound):
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, instance)
}
