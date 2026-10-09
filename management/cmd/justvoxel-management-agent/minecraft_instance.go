package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

const minecraftInstanceDirectory = "/var/lib/justvoxel/instances"

var (
	instanceIDPattern     = regexp.MustCompile(`^jv-[a-z0-9]{6,12}$`)
	instanceSuffixPattern = regexp.MustCompile(`^[a-zA-Z0-9]{6,12}$`)
	errInstanceRegistered = errors.New("Instance ID is already registered and cannot be changed.")
	errInstanceDuplicate  = errors.New("Instance ID is already used. Choose another value.")
	errInstanceSuffix     = errors.New("Use 6-12 letters and numbers after jv-.")
	minecraftInstances    = &minecraftInstanceStore{directory: minecraftInstanceDirectory, random: rand.Reader}
)

// Only identity lives here. Runtime configuration and archive contents never enter this store.
type minecraftInstanceStore struct {
	directory string
	random    io.Reader
	uid, gid  int // root in production; tests use their own temporary directory owner
}

type minecraftInstanceIdentity struct {
	ID     string `json:"id,omitempty"`
	Status string `json:"status"`
}

type minecraftInstanceRecord struct {
	ID string `json:"id"`
}

func (s *minecraftInstanceStore) private(info os.FileInfo, mode os.FileMode) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == s.uid && int(stat.Gid) == s.gid && info.Mode().Perm() == mode && info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0
}

func (s *minecraftInstanceStore) openDirectory(create bool) (*os.File, error) {
	if create {
		// The appliance state parent already exists. Do not create or reorganize runtime paths.
		if err := os.Mkdir(s.directory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	dir, err := os.OpenFile(s.directory, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	info, err := dir.Stat()
	if err != nil || !s.private(info, 0700) {
		dir.Close()
		return nil, errors.New("instance directory must be private and owned by root")
	}
	return dir, nil
}

func (s *minecraftInstanceStore) readRecord(name string) (string, error) {
	file, err := os.OpenFile(filepath.Join(s.directory, name), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !s.private(info, 0600) {
		return "", errors.New("invalid instance record permissions")
	}
	data, err := io.ReadAll(io.LimitReader(file, 1025))
	if err != nil {
		return "", err
	}
	if len(data) > 1024 {
		return "", errors.New("invalid instance record")
	}
	var record minecraftInstanceRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return "", err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || !instanceIDPattern.MatchString(record.ID) {
		return "", errors.New("invalid instance record")
	}
	return record.ID, nil
}

// Read never creates directories, repairs permissions, or registers an identity.
func (s *minecraftInstanceStore) read() (minecraftInstanceIdentity, error) {
	unavailable := minecraftInstanceIdentity{Status: "unavailable"}
	dir, err := s.openDirectory(false)
	if errors.Is(err, os.ErrNotExist) {
		return unavailable, nil
	}
	if err != nil {
		return unavailable, err
	}
	defer dir.Close()
	id, err := s.readRecord("minecraft.json")
	if errors.Is(err, os.ErrNotExist) {
		return unavailable, nil
	}
	if err != nil {
		return unavailable, err
	}
	return minecraftInstanceIdentity{ID: id, Status: "available"}, nil
}

// Retire only after a logical Minecraft instance has been deleted and verified.
func (s *minecraftInstanceStore) retire() error {
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
	// Unlink relative to the checked directory, without following a record symlink
	// or removing a directory in place of the record.
	if err := syscall.Unlinkat(int(dir.Fd()), "minecraft.json"); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return dir.Sync()
}

func (s *minecraftInstanceStore) register(suffix string, manual bool) (minecraftInstanceIdentity, error) {
	if manual && !instanceSuffixPattern.MatchString(suffix) {
		return minecraftInstanceIdentity{}, errInstanceSuffix
	}
	dir, err := s.openDirectory(true)
	if err != nil {
		return minecraftInstanceIdentity{}, err
	}
	defer dir.Close()
	// Lock the directory itself across Agent processes. No lock registry or retry loop.
	if err := syscall.Flock(int(dir.Fd()), syscall.LOCK_EX); err != nil {
		return minecraftInstanceIdentity{}, err
	}
	defer syscall.Flock(int(dir.Fd()), syscall.LOCK_UN)
	if id, err := s.readRecord("minecraft.json"); err == nil {
		if manual {
			return minecraftInstanceIdentity{}, errInstanceRegistered
		}
		return minecraftInstanceIdentity{ID: id, Status: "available"}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return minecraftInstanceIdentity{}, err
	}
	id := "jv-" + strings.ToLower(suffix)
	if !manual {
		// One secure random draw; base 36 uses the full lowercase alphanumeric alphabet.
		entropy := make([]byte, 16)
		if _, err := io.ReadFull(s.random, entropy); err != nil {
			return minecraftInstanceIdentity{}, err
		}
		space := new(big.Int).Exp(big.NewInt(36), big.NewInt(12), nil)
		value := new(big.Int).Mod(new(big.Int).SetBytes(entropy), space).Text(36)
		id = "jv-" + strings.Repeat("0", 12-len(value)) + value
	}
	entries, err := os.ReadDir(s.directory)
	if err != nil {
		return minecraftInstanceIdentity{}, err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		existing, err := s.readRecord(entry.Name())
		if err != nil {
			return minecraftInstanceIdentity{}, err
		}
		if existing == id {
			return minecraftInstanceIdentity{}, errInstanceDuplicate
		}
	}
	data, err := json.Marshal(minecraftInstanceRecord{ID: id})
	if err != nil {
		return minecraftInstanceIdentity{}, err
	}
	temp, err := os.CreateTemp(s.directory, ".minecraft-*")
	if err != nil {
		return minecraftInstanceIdentity{}, err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := temp.Chown(s.uid, s.gid); err != nil {
		return minecraftInstanceIdentity{}, err
	}
	if err := temp.Chmod(0600); err != nil {
		return minecraftInstanceIdentity{}, err
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		return minecraftInstanceIdentity{}, err
	}
	if err := temp.Sync(); err != nil {
		return minecraftInstanceIdentity{}, err
	}
	if err := temp.Close(); err != nil {
		return minecraftInstanceIdentity{}, err
	}
	// Publish the complete record atomically; Link cannot replace an existing ID.
	if err := os.Link(temp.Name(), filepath.Join(s.directory, "minecraft.json")); err != nil {
		return minecraftInstanceIdentity{}, err
	}
	if err := dir.Sync(); err != nil {
		return minecraftInstanceIdentity{}, err
	}
	return minecraftInstanceIdentity{ID: id, Status: "available"}, nil
}

func (s *server) minecraftInstanceRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireReadAccess(w, r); !ok {
		return
	}
	identity, err := minecraftInstances.read()
	if err != nil {
		identity = minecraftInstanceIdentity{Status: "unavailable"}
	}
	writeJSON(w, http.StatusOK, identity)
}

func (s *server) minecraftInstanceRegister(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	manual := strings.HasSuffix(r.URL.Path, "/manual")
	suffix := ""
	if manual {
		var request struct {
			Suffix string `json:"suffix"`
		}
		if !decodeJSON(w, r, &request) {
			return
		}
		suffix = request.Suffix
	} else {
		var request struct{}
		if !decodeJSON(w, r, &request) {
			return
		}
	}
	if !manual {
		if identity, err := minecraftInstances.read(); err == nil && identity.Status == "available" {
			writeJSON(w, http.StatusOK, identity)
			return
		}
	}
	identity, err := minecraftInstances.register(suffix, manual)
	if err != nil {
		status, message := http.StatusServiceUnavailable, "Instance ID registration failed: "+err.Error()
		switch {
		case errors.Is(err, errInstanceSuffix):
			status, message = http.StatusBadRequest, err.Error()
		case errors.Is(err, errInstanceRegistered), errors.Is(err, errInstanceDuplicate):
			status, message = http.StatusConflict, err.Error()
		}
		writeError(w, status, message)
		return
	}
	writeJSON(w, http.StatusOK, identity)
}
