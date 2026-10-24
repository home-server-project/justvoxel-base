package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/sys/unix"
)

var curseForgeDirectory = "/var/lib/justvoxel/management/curseforge"
var curseForgeMu sync.Mutex

const curseForgeKeyLimit = 1024
const curseForgeKeyFile = "api-key"

var errCurseForgeStorage = errors.New("CurseForge API configuration is unavailable")

type curseForgeStatus struct {
	Configured bool `json:"configured"`
}

func validCurseForgeKey(key string) bool {
	if len(key) == 0 || len(key) > curseForgeKeyLimit || strings.TrimSpace(key) == "" {
		return false
	}
	for _, c := range key {
		if unicode.IsControl(c) || c == '\u2028' || c == '\u2029' {
			return false
		}
	}
	return true
}

// Walk from / using directory descriptors: no component may be a symlink.
// Only this feature's final private directory is created; appliance parents exist.
func openCurseForgeDirectory(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errCurseForgeStorage
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errCurseForgeStorage
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	created := false
	for i, part := range parts {
		if i == len(parts)-1 {
			if err = unix.Mkdirat(fd, part, 0700); err != nil && err != unix.EEXIST {
				unix.Close(fd)
				return nil, errCurseForgeStorage
			}
			created = err == nil
		}
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if openErr != nil {
			return nil, errCurseForgeStorage
		}
		fd = next
	}
	dir := os.NewFile(uintptr(fd), path)
	// The service runs with group justvoxel-web. Normalize only the directory
	// we just created; tmpfiles owns boot repair of the known appliance path.
	if created {
		if unix.Fchown(fd, 0, 0) != nil || unix.Fchmod(fd, 0700) != nil {
			dir.Close()
			return nil, errCurseForgeStorage
		}
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != 0 || st.Gid != 0 || st.Mode&07777 != 0700 {
		dir.Close()
		return nil, errCurseForgeStorage
	}
	return dir, nil
}

func curseForgePresent(dir *os.File) (bool, error) {
	fd, err := unix.Openat(int(dir.Fd()), curseForgeKeyFile, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err == unix.ENOENT {
		return false, nil
	}
	if err != nil {
		return false, errCurseForgeStorage
	}
	f := os.NewFile(uintptr(fd), curseForgeKeyFile)
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0600 || st.Uid != 0 || st.Gid != 0 || st.Nlink != 1 || st.Size < 1 || st.Size > curseForgeKeyLimit {
		return false, errCurseForgeStorage
	}
	// Presence does not read or return the credential.
	return true, nil
}

func replaceCurseForgeKey(dir *os.File, key string) error {
	if !validCurseForgeKey(key) {
		return errCurseForgeStorage
	}
	if _, err := curseForgePresent(dir); err != nil {
		return err
	}
	name := ".api-key-" + rand.Text()
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return errCurseForgeStorage
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	defer unix.Unlinkat(int(dir.Fd()), name, 0)
	if f.Chown(0, 0) != nil || f.Chmod(0600) != nil {
		return errCurseForgeStorage
	}
	if _, err = io.WriteString(f, key); err != nil {
		return errCurseForgeStorage
	}
	if f.Sync() != nil {
		return errCurseForgeStorage
	}
	if err = unix.Renameat(int(dir.Fd()), name, int(dir.Fd()), curseForgeKeyFile); err != nil {
		return errCurseForgeStorage
	}
	if dir.Sync() != nil {
		return errCurseForgeStorage
	}
	return nil
}

func (s *server) adminCurseForgeAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, 400, "Invalid CurseForge API request")
		return
	}
	var request struct {
		Key string `json:"key"`
	}
	if r.Method == http.MethodPut {
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
		d.DisallowUnknownFields()
		if d.Decode(&request) != nil || d.Decode(new(any)) != io.EOF || !validCurseForgeKey(request.Key) {
			writeError(w, 400, "Enter a valid API key (1–1024 bytes, without line breaks or control characters).")
			return
		}
	}
	curseForgeMu.Lock()
	defer curseForgeMu.Unlock()
	dir, err := openCurseForgeDirectory(curseForgeDirectory)
	if err != nil {
		writeError(w, 503, errCurseForgeStorage.Error())
		return
	}
	defer dir.Close()
	configured, err := curseForgePresent(dir)
	if err == nil {
		switch r.Method {
		case http.MethodPut:
			err = replaceCurseForgeKey(dir, request.Key)
			configured = err == nil
		case http.MethodDelete:
			if configured {
				err = unix.Unlinkat(int(dir.Fd()), curseForgeKeyFile, 0)
				if err == nil {
					err = dir.Sync()
				}
			}
			configured = false
		}
	}
	if err != nil {
		writeError(w, 503, errCurseForgeStorage.Error())
		return
	}
	writeJSON(w, 200, curseForgeStatus{Configured: configured})
}
