package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	managementAPI = "v1"
	stateDir      = "/var/lib/justvoxel/webui"
	metadataPath  = "/usr/lib/justvoxel/webui-release.json"
	statusHelper  = "/usr/libexec/justvoxel/mjust/web-status-json"
	passwordChars = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
)

const (
	argonMemory      = 64 * 1024
	argonIterations  = 3
	argonParallelism = 2
	argonKeyLength   = 32
)

type releaseMetadata struct {
	Version       string `json:"version"`
	SourceCommit  string `json:"source_commit"`
	ManagementAPI string `json:"management_api"`
	BuildDate     string `json:"build_date"`
}

type session struct {
	Username   string
	Role       principalRole
	AuthSource authSource
	WebUserID  int64

	Created    time.Time
	LastSeen   time.Time
	MustChange bool
}

type server struct {
	webUID     uint32
	store      *webUIStore
	operations *operationStore

	mu       sync.Mutex
	sessions map[string]session
	failures []time.Time
	lockTill time.Time
}

type peerUIDKey struct{}

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		fatal("usage: justvoxel-management-agent <serve|bootstrap|password-reset|version>")
	}

	switch args[0] {
	case "bootstrap":
		password, created, err := bootstrap()
		if err != nil {
			fatal("bootstrap: %v", err)
		}
		if created {
			fmt.Println(password)
		}
	case "password-reset":
		password, err := resetPassword()
		if err != nil {
			fatal("password reset: %v", err)
		}
		fmt.Println(password)
	case "serve":
		socket := "/run/justvoxel/management.sock"
		if len(args) > 2 || (len(args) == 2 && args[1] == "") {
			fatal("usage: justvoxel-management-agent serve [socket]")
		}
		if len(args) == 2 {
			socket = args[1]
		}
		if err := serve(socket); err != nil {
			fatal("serve: %v", err)
		}
	case "version":
		meta, _ := readReleaseMetadata()
		fmt.Printf("JustVoxel Management API %s\nWebUI %s\n", managementAPI, valueOr(meta.Version, "unknown"))
	default:
		fatal("unknown command %q", args[0])
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(1)
}

func bootstrap() (string, bool, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return "", false, err
	}
	if _, err := currentAuthMode(); err != nil {
		return "", false, err
	}
	return "", false, nil
}

func resetPassword() (string, error) {
	mode, err := currentAuthMode()
	if err != nil {
		return "", err
	}
	if mode != authModeSeparate {
		return "", errors.New("System account mode uses the Linux voxel password; use passwd voxel")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return "", err
	}
	password, err := randomPassword(14)
	if err != nil {
		return "", err
	}
	if err := writeLocalAdministrator(password); err != nil {
		return "", err
	}
	return password, nil
}

func serve(socket string) error {
	if _, _, err := bootstrap(); err != nil {
		return err
	}
	store, err := openWebUIStore(webUIDatabasePath)
	if err != nil {
		return fmt.Errorf("open WebUI identity database: %w", err)
	}
	defer store.close()

	operations, err := openOperationStore(operationStateDir)
	if err != nil {
		return fmt.Errorf("open operation journal: %w", err)
	}
	defer operations.close()

	webAccount, err := user.Lookup("justvoxel-web")
	if err != nil {
		return fmt.Errorf("lookup justvoxel-web: %w", err)
	}
	uid64, err := strconv.ParseUint(webAccount.Uid, 10, 32)
	if err != nil {
		return fmt.Errorf("parse justvoxel-web uid: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o750); err != nil {
		return err
	}
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.Chmod(socket, 0o660); err != nil {
		return err
	}

	s := &server{webUID: uint32(uid64), store: store, operations: operations, sessions: make(map[string]session)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/auth/login", s.providerLogin)
	mux.HandleFunc("POST /v1/auth/logout", s.logout)
	mux.HandleFunc("POST /v1/auth/password", s.providerChangePassword)
	mux.HandleFunc("GET /v1/auth", s.authStatus)
	mux.HandleFunc("POST /v1/auth/mode", s.changeAuthMode)
	mux.HandleFunc("GET /v1/session", s.sessionStatus)
	mux.HandleFunc("GET /v1/info", s.info)
	mux.HandleFunc("GET /v1/status", s.status)
	mux.HandleFunc("POST /v1/backups/manual", s.manualBackup)
	registerAdminUserRoutes(mux, s)
	registerAdminActivityRoutes(mux, s)
	registerAdminDiscoveryRoutes(mux, s)
	registerAdminConfigurationRoutes(mux, s)
	registerAdminValidationRoutes(mux, s)
	registerAdminRestoreRoutes(mux, s)
	registerAdminMigrationExportRoutes(mux, s)
	registerOperationalRoutes(mux, s)
	registerMinecraftRoutes(mux, s)
	registerAdminSystemActionRoutes(mux, s)

	httpServer := &http.Server{
		Handler:           s.requirePeer(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      210 * time.Second,
		IdleTimeout:       60 * time.Second,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			uid, err := peerUID(c)
			if err != nil {
				uid = ^uint32(0)
			}
			return context.WithValue(ctx, peerUIDKey{}, uid)
		},
	}
	log.Printf("JustVoxel Management API %s listening on %s", managementAPI, socket)
	return httpServer.Serve(listener)
}

func (s *server) requirePeer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, ok := r.Context().Value(peerUIDKey{}).(uint32)
		if !ok || (uid != 0 && uid != s.webUID) {
			writeError(w, http.StatusForbidden, "peer not permitted")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func peerUID(conn net.Conn) (uint32, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, errors.New("not a Unix connection")
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *syscall.Ucred
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		cred, sockErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if sockErr != nil {
		return 0, sockErr
	}
	return cred.Uid, nil
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	token, _, ok := s.authorize(r, true)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid session")
		return
	}
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) info(w http.ResponseWriter, _ *http.Request) {
	meta, _ := readReleaseMetadata()
	variant, _ := os.ReadFile("/usr/lib/justvoxel/variant")
	writeJSON(w, http.StatusOK, map[string]string{
		"management_api": managementAPI,
		"webui_version":  valueOr(meta.Version, "unknown"),
		"variant":        strings.TrimSpace(string(variant)),
	})
}

func (s *server) status(w http.ResponseWriter, r *http.Request) {
	_, sess, ok := s.authorize(r, false)
	if !ok {
		if sess.MustChange {
			writeError(w, http.StatusForbidden, "password change required")
		} else {
			writeError(w, http.StatusUnauthorized, "invalid session")
		}
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	args := []string{}
	if r.URL.Query().Get("details") == "1" {
		args = append(args, "--details")
	}
	cmd := exec.CommandContext(ctx, statusHelper, args...)
	output, err := cmd.Output()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "status collection failed")
		return
	}
	if !json.Valid(output) {
		writeError(w, http.StatusInternalServerError, "status collector returned invalid data")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(output)
}

func (s *server) authorize(r *http.Request, allowMustChange bool) (string, session, bool) {
	if sess, ok := localRootAdministrator(r); ok {
		return "", sess, true
	}
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return "", session{}, false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	if token == "" {
		return token, session{}, false
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[token]
	if !ok {
		return token, session{}, false
	}
	if now.Sub(sess.Created) > 12*time.Hour || now.Sub(sess.LastSeen) > 30*time.Minute {
		delete(s.sessions, token)
		return token, session{}, false
	}
	if sess.MustChange && !allowMustChange {
		return token, sess, false
	}
	sess.LastSeen = now
	s.sessions[token] = sess
	return token, sess, true
}

func (s *server) loginDelay() time.Duration {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.Before(s.lockTill) {
		return time.Until(s.lockTill)
	}
	cutoff := now.Add(-5 * time.Minute)
	kept := s.failures[:0]
	for _, failure := range s.failures {
		if failure.After(cutoff) {
			kept = append(kept, failure)
		}
	}
	s.failures = kept
	return 0
}

func (s *server) recordFailure() {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, now)
	cutoff := now.Add(-5 * time.Minute)
	count := 0
	for _, failure := range s.failures {
		if failure.After(cutoff) {
			count++
		}
	}
	if count >= 5 {
		s.lockTill = now.Add(30 * time.Second)
	}
}

func (s *server) clearFailures() {
	s.mu.Lock()
	s.failures = nil
	s.lockTill = time.Time{}
	s.mu.Unlock()
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func randomPassword(length int) (string, error) {
	out := make([]byte, length)
	limit := byte(256 - (256 % len(passwordChars)))
	for i := range out {
		for {
			var b [1]byte
			if _, err := rand.Read(b[:]); err != nil {
				return "", err
			}
			if b[0] < limit {
				out[i] = passwordChars[int(b[0])%len(passwordChars)]
				break
			}
		}
	}
	return string(out), nil
}

func randomToken(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func readReleaseMetadata() (releaseMetadata, error) {
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return releaseMetadata{}, err
	}
	var meta releaseMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return releaseMetadata{}, err
	}
	return meta, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
