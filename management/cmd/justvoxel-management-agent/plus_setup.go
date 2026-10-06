package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const plusSetupDirectory = "/var/lib/justvoxel-plus-setup"
const plusDeployedMarker = "/etc/justvoxel/plus/deployed"
const plusSetupBodyLimit = 128 * 1024

type plusSetupComponents struct {
	Database bool `json:"database"`
	Cache    bool `json:"cache"`
	Panel    bool `json:"panel"`
	Wings    bool `json:"wings"`
	Drydock  bool `json:"drydock"`
}

type plusSetupRequest struct {
	Components      plusSetupComponents `json:"components"`
	UseDomain       bool                `json:"use_domain"`
	Host            string              `json:"host"`
	UseTLS          bool                `json:"use_tls"`
	Certificate     string              `json:"certificate"`
	PrivateKey      string              `json:"private_key"`
	StorageMount    string              `json:"storage_mount"`
	SeparateAccount bool                `json:"separate_account"`
	Username        string              `json:"username"`
	Email           string              `json:"email"`
	Password        string              `json:"password"`
}

type plusSetupMount struct {
	Target  string `json:"target"`
	FSType  string `json:"fstype"`
	Options string `json:"options,omitempty"`
	Source  string `json:"source,omitempty"`
}

var readPlusSetupMounts = func(ctx context.Context) ([]plusSetupMount, error) {
	data, err := exec.CommandContext(ctx, "findmnt", "--json", "--list", "--output", "TARGET,FSTYPE,OPTIONS,SOURCE").Output()
	if err != nil {
		return nil, err
	}
	var out struct {
		Filesystems []plusSetupMount `json:"filesystems"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return eligiblePlusSetupMounts(out.Filesystems), nil
}

func eligiblePlusSetupMounts(mounts []plusSetupMount) []plusSetupMount {
	out := []plusSetupMount{}
	for _, mount := range mounts {
		if mount.FSType != "ext4" && mount.FSType != "xfs" && mount.FSType != "btrfs" {
			continue
		}
		if !strings.HasPrefix(mount.Source, "/dev/") || !strings.Contains(","+mount.Options+",", ",rw,") {
			continue
		}
		if !(strings.HasPrefix(mount.Target, "/mnt/") || strings.HasPrefix(mount.Target, "/srv/") || strings.HasPrefix(mount.Target, "/media/") || strings.HasPrefix(mount.Target, "/var/mnt/") || strings.HasPrefix(mount.Target, "/var/srv/")) {
			continue
		}
		if filepath.Clean(mount.Target) != mount.Target || strings.ContainsAny(mount.Target, "\r\n\x00$") {
			continue
		}
		// Return only display information, never device or mount options to the browser.
		out = append(out, plusSetupMount{Target: mount.Target, FSType: mount.FSType})
	}
	return out
}

func plusSetupDeployed() (bool, error) {
	_, err := os.Stat(plusDeployedMarker)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (s *server) plusSetupState(w http.ResponseWriter, r *http.Request) {
	if !s.plus {
		http.NotFound(w, r)
		return
	}
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	mounts, err := readPlusSetupMounts(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "storage information is unavailable")
		return
	}
	deployed, err := plusSetupDeployed()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "setup state is unavailable")
		return
	}
	_, err = os.Lstat(filepath.Join(plusSetupDirectory, "setup.json"))
	if err != nil && !os.IsNotExist(err) {
		writeError(w, http.StatusServiceUnavailable, "setup state is unavailable")
		return
	}
	prepared := err == nil
	p := defaultPlusDeploymentPaths()
	deployment, err := readPlusDeploymentStatus(p)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "deployment progress is unavailable")
		return
	}
	running := plusSetupUnitRunning(ctx)
	if deployment.State == "running" && !running {
		deployment.State = "failed"
		deployment.Message = "Setup was interrupted. Existing data is retained. Fix the cause and retry deployment."
	}
	_, runtimeErr := os.Lstat(filepath.Join(p.Configuration, "compose.yaml"))
	writeJSON(w, http.StatusOK, map[string]any{"prepared": prepared, "deployed": deployed, "mounts": mounts, "deployment": deployment, "running": running, "locked": runtimeErr == nil})
}

func decodePlusSetup(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, plusSetupBodyLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid setup request")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid setup request")
		return false
	}
	return true
}

var plusSetupUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
var plusSetupHostnameLabel = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

func validatePlusSetup(request *plusSetupRequest, hostUsername string, mounts []plusSetupMount, now time.Time) error {
	c := request.Components
	if !c.Database && !c.Cache && !c.Panel && !c.Wings && !c.Drydock {
		return errors.New("enable at least one component, or choose Look around")
	}
	request.Host = strings.TrimSpace(request.Host)
	if request.Host == "" || len(request.Host) > 253 {
		return errors.New("provide the hostname or IP address used to open the applications")
	}
	ip := net.ParseIP(request.Host)
	if ip != nil && ip.To4() == nil {
		return errors.New("use an IPv4 address or hostname for the initial application setup")
	}
	if ip == nil {
		for _, label := range strings.Split(request.Host, ".") {
			if !plusSetupHostnameLabel.MatchString(label) {
				return errors.New("enter a hostname or IP address without a scheme, port or path")
			}
		}
	}
	if request.UseDomain && (ip != nil || !strings.Contains(request.Host, ".")) {
		return errors.New("provide your domain name")
	}
	if request.UseTLS {
		pair, err := tls.X509KeyPair([]byte(request.Certificate), []byte(request.PrivateKey))
		if err != nil || len(pair.Certificate) == 0 {
			return errors.New("provide a PEM certificate and its matching private key")
		}
		certificate, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil || now.Before(certificate.NotBefore) || !now.Before(certificate.NotAfter) {
			return errors.New("the certificate must be currently valid")
		}
		if err := certificate.VerifyHostname(request.Host); err != nil {
			return errors.New("the certificate must cover the chosen hostname or IP address")
		}
	} else {
		request.Certificate = ""
		request.PrivateKey = ""
	}
	if request.StorageMount != "" {
		found := false
		for _, mount := range mounts {
			if request.StorageMount == mount.Target {
				found = true
				break
			}
		}
		if !found {
			return errors.New("choose an available mounted local disk, or use system storage")
		}
		resolved, err := filepath.EvalSymlinks(request.StorageMount)
		if err != nil || resolved != request.StorageMount {
			return errors.New("the storage mount must be available without symbolic links")
		}
	}
	if c.Panel || c.Drydock {
		if !request.SeparateAccount {
			request.Username = hostUsername
		}
		if !plusSetupUsernamePattern.MatchString(request.Username) {
			return errors.New("application username must use 1 to 64 letters, numbers, dots, hyphens or underscores; choose a separate account if needed")
		}
		if len(request.Password) < 12 || len(request.Password) > 128 || strings.ContainsAny(request.Password, "\x00\r\n") {
			return errors.New("application password must be 12 to 128 characters without line breaks")
		}
		if c.Panel {
			address, err := mail.ParseAddress(request.Email)
			if err != nil || address.Address != request.Email || len(request.Email) > 254 {
				return errors.New("provide a valid email address for the Panel administrator")
			}
		} else {
			request.Email = ""
		}
	} else {
		request.Username = ""
		request.Email = ""
		request.Password = ""
		request.SeparateAccount = false
	}
	return nil
}

// The deployment stage consumes this root-only preparation file. It must remove
// the application password after creating the independent upstream accounts.
func savePlusSetup(directory string, request plusSetupRequest) error {
	parent := filepath.Dir(directory)
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil || resolved != parent {
		return errors.New("unsafe setup parent directory")
	}
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return err
	}
	parentStat, ok := parentInfo.Sys().(*syscall.Stat_t)
	if !ok || parentStat.Uid != uint32(os.Geteuid()) || parentInfo.Mode().Perm()&0022 != 0 {
		return errors.New("unsafe setup parent directory")
	}
	if err := os.Mkdir(directory, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return errors.New("unsafe setup directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("unsafe setup directory owner")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".setup-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(file.Name(), filepath.Join(directory, "setup.json")); err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *server) plusSetupPrepare(w http.ResponseWriter, r *http.Request) {
	if !s.plus {
		http.NotFound(w, r)
		return
	}
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	var request plusSetupRequest
	if !decodePlusSetup(w, r, &request) {
		return
	}
	s.plusSetupMu.Lock()
	defer s.plusSetupMu.Unlock()
	if plusSetupUnitRunning(r.Context()) {
		writeError(w, http.StatusConflict, "setup is already running")
		return
	}
	lock, err := plusPreparationLock(plusSetupDirectory)
	if err != nil {
		writeError(w, http.StatusConflict, "setup is already running")
		return
	}
	defer lock.Close()
	if _, err := os.Lstat(filepath.Join(plusConfigurationDirectory, "compose.yaml")); err == nil {
		writeError(w, http.StatusConflict, "deployment has started; retry the saved choices or manage configuration as administrator")
		return
	} else if !os.IsNotExist(err) {
		writeError(w, http.StatusServiceUnavailable, "setup state is unavailable")
		return
	}
	deployed, err := plusSetupDeployed()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "setup state is unavailable")
		return
	}
	if deployed {
		writeError(w, http.StatusConflict, "this appliance is already deployed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	mounts, err := readPlusSetupMounts(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "storage information is unavailable")
		return
	}
	if err := validatePlusSetup(&request, actor.Username, mounts, time.Now()); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := savePlusSetup(plusSetupDirectory, request); err != nil {
		writeError(w, http.StatusInternalServerError, "setup preparation could not be saved")
		return
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "prepare_plus_setup", "plus", true, "setup choices prepared")
	}
	// Deliberately return no request fields, credentials, certificates or key material.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "prepared": true, "message": "Setup choices saved. Ready to deploy the selected applications."})
}
