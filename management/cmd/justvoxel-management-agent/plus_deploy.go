package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/argon2"
)

const plusConfigurationDirectory = "/etc/justvoxel/plus"
const plusSetupUnit = "justvoxel-plus-setup.service"

type plusDeploymentPaths struct{ Configuration, Preparation, Templates, DefaultData, Socket, CABundle, UnitOverride string }

func defaultPlusDeploymentPaths() plusDeploymentPaths {
	return plusDeploymentPaths{plusConfigurationDirectory, plusSetupDirectory, "/usr/share/justvoxel/templates/plus", "/var/lib/justvoxel-plus", "/var/run/docker.sock", "/etc/pki/tls/certs/ca-bundle.crt", "/etc/systemd/system/justvoxel-plus-stack.service.d"}
}

type plusDeploymentStatus struct {
	State      string              `json:"state"`
	Stage      string              `json:"stage"`
	Message    string              `json:"message"`
	Started    bool                `json:"started"`
	PanelURL   string              `json:"panel_url,omitempty"`
	DrydockURL string              `json:"drydock_url,omitempty"`
	Components plusSetupComponents `json:"components"`
}

type plusBoundedOutput struct{ bytes.Buffer }

func (b *plusBoundedOutput) Write(p []byte) (int, error) {
	count := len(p)
	if left := 64*1024 - b.Len(); left > 0 {
		if len(p) > left {
			p = p[:left]
		}
		_, _ = b.Buffer.Write(p)
	}
	return count, nil
}

var checkPlusHTTP = plusHTTPReady

var runPlusDeploymentCommand = func(ctx context.Context, command string, args []string, input []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	// Inherit no Compose override from the caller; the runtime files are authoritative.
	for _, entry := range os.Environ() {
		key := strings.SplitN(entry, "=", 2)[0]
		if strings.HasPrefix(key, "COMPOSE_") || strings.HasPrefix(key, "PLUS_") || strings.HasSuffix(key, "_IMAGE") || key == "DOCKER_HOST" || key == "DOCKER_CONTEXT" || key == "DOCKER_CONFIG" {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "DOCKER_HOST=unix:///var/run/docker.sock")
	cmd.Stdin = bytes.NewReader(input)
	var out, diagnostic plusBoundedOutput
	cmd.Stdout = &out
	cmd.Stderr = &diagnostic
	err := cmd.Run()
	// Never attach upstream diagnostics to an error, journal or API response.
	return out.Bytes(), err
}

func plusCompose(ctx context.Context, p plusDeploymentPaths, input []byte, args ...string) ([]byte, error) {
	fixed := []string{"compose", "--project-name", "justvoxel-plus", "--env-file", filepath.Join(p.Configuration, "stack.env"), "--file", filepath.Join(p.Configuration, "compose.yaml")}
	return runPlusDeploymentCommand(ctx, "docker", append(fixed, args...), input)
}

var setPlusOwnership = os.Chown

func writePlusFile(path string, data []byte, mode os.FileMode, uid, gid int) error {
	info, err := os.Lstat(path)
	if err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return errors.New("unsafe runtime file")
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".plus-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(mode.Perm()); err == nil && (uid >= 0 || gid >= 0) {
		err = setPlusOwnership(file.Name(), uid, gid)
	}
	if err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closed := file.Close()
	if err != nil {
		return err
	}
	if closed != nil {
		return closed
	}
	return os.Rename(file.Name(), path)
}

func ensurePlusDirectory(path string, mode os.FileMode, uid, gid int) error {
	// Do not follow a symlink at any level of persistent paths.
	for current := filepath.Clean(path); current != "/"; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return errors.New("unsafe runtime directory")
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.MkdirAll(path, mode.Perm()); err != nil {
		return err
	}
	if uid >= 0 || gid >= 0 {
		if err := setPlusOwnership(path, uid, gid); err != nil {
			return err
		}
	}
	return os.Chmod(path, mode)
}

func plusPreparationLock(directory string) (*os.File, error) {
	if err := ensurePlusDirectory(directory, 0700, -1, -1); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(directory, "lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("setup is already running")
	}
	return file, nil
}

func readPlusJSON(path string, out any, secret bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > plusSetupBodyLimit {
		return errors.New("unsafe setup file")
	}
	if secret && info.Mode().Perm() != 0600 {
		return errors.New("unsafe setup permissions")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, plusSetupBodyLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("invalid setup file")
	}
	return nil
}

func savePlusDeploymentStatus(p plusDeploymentPaths, status plusDeploymentStatus) error {
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	return writePlusFile(filepath.Join(p.Preparation, "deployment.json"), data, 0600, -1, -1)
}

func readPlusDeploymentStatus(p plusDeploymentPaths) (plusDeploymentStatus, error) {
	var status plusDeploymentStatus
	err := readPlusJSON(filepath.Join(p.Preparation, "deployment.json"), &status, true)
	if os.IsNotExist(err) {
		return status, nil
	}
	return status, err
}

func plusSetupUnitRunning(ctx context.Context) bool {
	out, _ := runPlusDeploymentCommand(ctx, "systemctl", []string{"is-active", plusSetupUnit}, nil)
	state := strings.TrimSpace(string(out))
	return state == "active" || state == "activating"
}

func plusApplicationURL(host string, tlsEnabled bool, port int) string {
	scheme := "http"
	if tlsEnabled {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port))
}

func plusSelectedServices(c plusSetupComponents) []string {
	services := []string{}
	for _, item := range []struct {
		name    string
		enabled bool
	}{{"database", c.Database}, {"cache", c.Cache}, {"panel", c.Panel}, {"wings", c.Wings}, {"drydock", c.Drydock}} {
		if item.enabled {
			services = append(services, item.name)
		}
	}
	return services
}

func renderPlusTemplate(p plusDeploymentPaths, name string, values map[string]string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(p.Templates, name))
	if err != nil {
		return nil, err
	}
	text := string(data)
	for key, value := range values {
		text = strings.ReplaceAll(text, "@@"+key+"@@", value)
	}
	if strings.Contains(text, "@@") {
		return nil, errors.New("incomplete runtime template")
	}
	return []byte(strings.ReplaceAll(text, plusConfigurationDirectory, p.Configuration)), nil
}

func plusRandomSecret(length int) (string, error) {
	data := make([]byte, length)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func plusDrydockHash(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonIterations, argonParallelism, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func plusDeploymentDataRoot(p plusDeploymentPaths, r plusSetupRequest) string {
	if r.StorageMount != "" {
		return filepath.Join(r.StorageMount, "justvoxel-plus")
	}
	return p.DefaultData
}

func preparePlusRuntime(p plusDeploymentPaths, r plusSetupRequest) error {
	socket, err := os.Stat(p.Socket)
	if err != nil {
		return errors.New("Docker socket is unavailable")
	}
	socketStat, ok := socket.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("Docker socket group is unavailable")
	}
	gid := int(socketStat.Gid)
	root := plusDeploymentDataRoot(p, r)
	if !regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`).MatchString(root) {
		return errors.New("the storage path is not supported by upstream Wings; use a mount path without spaces or special characters")
	}
	if _, err := os.Lstat(filepath.Join(p.Configuration, "compose.yaml")); err == nil {
		// An explicit retry preserves generated database credentials and administrator
		// image selections. Never rotate credentials or overwrite existing stack files.
		for _, name := range []string{"stack.env", "database.env", "panel.env", "drydock.env"} {
			info, err := os.Lstat(filepath.Join(p.Configuration, name))
			if err != nil || !info.Mode().IsRegular() {
				return errors.New("existing runtime configuration needs administrator attention")
			}
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if entries, err := os.ReadDir(root); err == nil && len(entries) != 0 {
		return errors.New("choose an empty application directory; existing data will not be overwritten")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := ensurePlusDirectory(p.Configuration, os.ModeSetgid|0770, os.Geteuid(), gid); err != nil {
		return err
	}
	if err := ensurePlusDirectory(root, 0755, os.Geteuid(), os.Getegid()); err != nil {
		return err
	}
	for _, name := range []string{"database", "redis", "panel/logs", "panel/nginx", "certificates", "wings", "wings/data", "wings/logs"} {
		if err := ensurePlusDirectory(filepath.Join(root, name), 0755, -1, -1); err != nil {
			return err
		}
	}
	for _, entry := range []struct {
		path string
		uid  int
		mode os.FileMode
	}{{"panel/var", 82, 0700}, {"drydock", 1000, 0700}, {"certificates/drydock", 1000, 0700}} {
		if err := ensurePlusDirectory(filepath.Join(root, entry.path), entry.mode, entry.uid, entry.uid); err != nil {
			return err
		}
	}
	if err := ensurePlusDirectory(filepath.Join(p.Configuration, "wings"), 0700, -1, -1); err != nil {
		return err
	}
	if err := ensurePlusDirectory(filepath.Join(p.Configuration, "trust"), 0755, -1, -1); err != nil {
		return err
	}
	databasePassword, err := plusRandomSecret(32)
	if err != nil {
		return err
	}
	rootPassword, err := plusRandomSecret(32)
	if err != nil {
		return err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	salt, err := plusRandomSecret(24)
	if err != nil {
		return err
	}
	hash, err := plusDrydockHash(r.Password)
	if err != nil {
		return err
	}
	panelPort := 8081
	if r.UseTLS {
		panelPort = 8443
	}
	bind := "0.0.0.0"
	if r.UseTLS {
		bind = "127.0.0.1"
	}
	values := map[string]string{"DATA_ROOT": root, "PROFILES": strings.Join(plusSelectedServices(r.Components), ","), "PLUS_HOST": r.Host, "PANEL_URL": plusApplicationURL(r.Host, r.UseTLS, panelPort), "PANEL_HTTP_BIND_ADDRESS": bind, "DOCKER_GID": strconv.Itoa(gid), "DATABASE_PASSWORD": databasePassword, "DATABASE_ROOT_PASSWORD": rootPassword, "PANEL_APP_KEY": "base64:" + base64.StdEncoding.EncodeToString(key), "PANEL_HASHIDS_SALT": salt, "DRYDOCK_USER": r.Username, "DRYDOCK_PASSWORD_HASH": hash, "TLS_ENABLED": strconv.FormatBool(r.UseTLS), "TLS_LISTENER": "", "TLS_CONFIGURATION": ""}
	if r.UseTLS {
		certPath := filepath.Join(root, "certificates", "live", strings.ToLower(r.Host))
		if err := ensurePlusDirectory(certPath, 0700, -1, -1); err != nil {
			return err
		}
		for _, file := range []struct{ name, value string }{{"fullchain.pem", r.Certificate}, {"privkey.pem", r.PrivateKey}} {
			if err := writePlusFile(filepath.Join(certPath, file.name), []byte(file.value), 0600, -1, -1); err != nil {
				return err
			}
			if err := writePlusFile(filepath.Join(root, "certificates/drydock", file.name), []byte(file.value), 0400, 1000, 1000); err != nil {
				return err
			}
		}
		values["TLS_LISTENER"] = "listen 443 ssl;"
		values["TLS_CONFIGURATION"] = "ssl_certificate /etc/letsencrypt/live/" + strings.ToLower(r.Host) + "/fullchain.pem;\n    ssl_certificate_key /etc/letsencrypt/live/" + strings.ToLower(r.Host) + "/privkey.pem;\n    ssl_protocols TLSv1.2 TLSv1.3;"
	}
	bundle, err := os.ReadFile(p.CABundle)
	if err != nil {
		return errors.New("host certificate authorities are unavailable")
	}
	if r.UseTLS {
		bundle = append(bundle, []byte("\n"+r.Certificate)...)
	}
	if err := writePlusFile(filepath.Join(p.Configuration, "trust/ca-bundle.crt"), bundle, 0644, -1, -1); err != nil {
		return err
	}
	ini := []byte("openssl.cafile=/etc/justvoxel-plus/trust/ca-bundle.crt\ncurl.cainfo=/etc/justvoxel-plus/trust/ca-bundle.crt\n")
	if err := writePlusFile(filepath.Join(p.Configuration, "trust/php-ca.ini"), ini, 0644, -1, -1); err != nil {
		return err
	}
	for _, name := range []string{"stack.env", "database.env", "panel.env", "drydock.env"} {
		data, err := renderPlusTemplate(p, name+".in", values)
		if err != nil {
			return err
		}
		if err := writePlusFile(filepath.Join(p.Configuration, name), data, 0600, -1, -1); err != nil {
			return err
		}
	}
	persistent, err := renderPlusTemplate(p, "panel-persistent.env.in", values)
	if err != nil {
		return err
	}
	if err := writePlusFile(filepath.Join(root, "panel/var/.env"), persistent, 0600, 82, 82); err != nil {
		return err
	}
	nginx, err := renderPlusTemplate(p, "panel-nginx.conf.in", values)
	if err != nil {
		return err
	}
	if err := writePlusFile(filepath.Join(root, "panel/nginx/panel.conf"), nginx, 0644, -1, -1); err != nil {
		return err
	}
	bootstrap, err := renderPlusTemplate(p, "panel-bootstrap.php", values)
	if err != nil {
		return err
	}
	if err := writePlusFile(filepath.Join(p.Configuration, "panel-bootstrap.php"), bootstrap, 0600, -1, -1); err != nil {
		return err
	}
	compose, err := renderPlusTemplate(p, "compose.yaml", values)
	if err != nil {
		return err
	}
	if err := ensurePlusDirectory(p.UnitOverride, 0755, -1, -1); err != nil {
		return err
	}
	unit := "[Unit]\nRequiresMountsFor=" + root + "\n"
	if r.StorageMount != "" {
		unit += "[Service]\nExecStartPre=/usr/bin/mountpoint -q " + r.StorageMount + "\n"
	}
	if err := writePlusFile(filepath.Join(p.UnitOverride, "storage.conf"), []byte(unit), 0644, -1, -1); err != nil {
		return err
	}
	// Compose is written last: its presence marks reusable runtime configuration.
	return writePlusFile(filepath.Join(p.Configuration, "compose.yaml"), compose, 0660, os.Geteuid(), gid)
}

func waitPlusCondition(ctx context.Context, limit time.Duration, check func(context.Context) bool) error {
	deadline, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if check(deadline) {
			return nil
		}
		select {
		case <-deadline.Done():
			return errors.New("application readiness timed out")
		case <-ticker.C:
		}
	}
}

func waitPlusContainer(ctx context.Context, p plusDeploymentPaths, service string, healthy bool) error {
	return waitPlusCondition(ctx, 6*time.Minute, func(ctx context.Context) bool {
		id, err := plusCompose(ctx, p, nil, "ps", "--all", "--quiet", service)
		if err != nil {
			return false
		}
		container := strings.TrimSpace(string(id))
		if !regexp.MustCompile(`^[a-f0-9]{12,64}$`).MatchString(container) {
			return false
		}
		state, err := runPlusDeploymentCommand(ctx, "docker", []string{"inspect", "--format", "{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{end}}", container}, nil)
		if err != nil {
			return false
		}
		fields := strings.Fields(string(state))
		return len(fields) > 0 && fields[0] == "running" && (!healthy || len(fields) > 1 && fields[1] == "healthy")
	})
}

func plusHTTPReady(ctx context.Context, r plusSetupRequest, port int, path string) bool {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if r.UseTLS {
		pool.AppendCertsFromPEM([]byte(r.Certificate))
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: r.Host}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 5 * time.Second, Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, plusApplicationURL("127.0.0.1", r.UseTLS, port)+path, nil)
	if err != nil {
		return false
	}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode >= 200 && response.StatusCode < 400
}

func plusNodeCapacity(root string) (int64, int64, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	var memory int64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && fields[0] == "MemTotal:" {
			memory, _ = strconv.ParseInt(fields[1], 10, 64)
			break
		}
	}
	memory = memory/1024 - 1024
	if memory < 512 {
		memory = 512
	}
	var disk syscall.Statfs_t
	if err := syscall.Statfs(root, &disk); err != nil {
		return 0, 0, err
	}
	available := int64(disk.Bavail) * disk.Bsize / (1024 * 1024) * 95 / 100
	if available < 1 {
		return 0, 0, errors.New("application disk is full")
	}
	return memory, available, nil
}

type plusPanelBootstrapResult struct {
	OK            bool           `json:"ok"`
	NodeID        int            `json:"node_id"`
	Configuration map[string]any `json:"configuration"`
}

func bootstrapPlusPanel(ctx context.Context, p plusDeploymentPaths, r plusSetupRequest) (plusPanelBootstrapResult, error) {
	root := plusDeploymentDataRoot(p, r)
	memory, disk, err := plusNodeCapacity(root)
	if err != nil {
		return plusPanelBootstrapResult{}, err
	}
	scheme := "http"
	if r.UseTLS {
		scheme = "https"
	}
	input, err := json.Marshal(map[string]any{"action": "bootstrap", "username": r.Username, "email": r.Email, "password": r.Password, "wings": r.Components.Wings, "host": r.Host, "scheme": scheme, "memory": memory, "disk": disk, "data_root": root})
	if err != nil {
		return plusPanelBootstrapResult{}, err
	}
	defer clear(input)
	data, err := plusCompose(ctx, p, input, "exec", "-T", "--user", "0", "panel", "php", "/run/justvoxel-plus-bootstrap.php")
	if err != nil {
		return plusPanelBootstrapResult{}, errors.New("Panel administrator or node creation failed")
	}
	var result plusPanelBootstrapResult
	if err := json.Unmarshal(data, &result); err != nil || !result.OK {
		return result, errors.New("Panel setup returned invalid configuration")
	}
	if r.Components.Wings {
		if result.NodeID <= 0 || result.Configuration["token"] == nil || result.Configuration["uuid"] == nil {
			return result, errors.New("Panel did not provide Wings credentials")
		}
		system, ok := result.Configuration["system"].(map[string]any)
		if !ok {
			return result, errors.New("Panel did not provide Wings storage configuration")
		}
		system["root_directory"] = filepath.Join(root, "wings")
		system["archive_directory"] = filepath.Join(root, "wings/archives")
		system["backup_directory"] = filepath.Join(root, "wings/backups")
		system["timezone"] = "UTC"
		network, err := preparePlusWingsNetwork(ctx)
		if err != nil {
			return result, err
		}
		result.Configuration["docker"] = map[string]any{"network": network}

		configuration, err := json.MarshalIndent(result.Configuration, "", "  ")
		if err != nil {
			return result, err
		}
		// JSON is valid YAML; preserve upstream node credentials and configuration.
		if err := writePlusFile(filepath.Join(p.Configuration, "wings/config.yml"), configuration, 0600, -1, -1); err != nil {
			return result, err
		}
	}
	return result, nil
}

func verifyPlusPanelWings(ctx context.Context, p plusDeploymentPaths, nodeID int) bool {
	input, _ := json.Marshal(map[string]any{"action": "verify", "node_id": nodeID})
	data, err := plusCompose(ctx, p, input, "exec", "-T", "--user", "0", "panel", "php", "/run/justvoxel-plus-bootstrap.php")
	if err != nil {
		return false
	}
	var result struct {
		OK bool `json:"ok"`
	}
	return json.Unmarshal(data, &result) == nil && result.OK
}

func activatePlusFirewall(ctx context.Context, c plusSetupComponents, tlsEnabled bool) error {
	ports := []int{}
	if c.Panel {
		if tlsEnabled {
			ports = append(ports, 8443)
		} else {
			ports = append(ports, 8081)
		}
	}
	if c.Wings {
		ports = append(ports, 8080, 2022)
	}
	if c.Drydock {
		ports = append(ports, 3000)
	}
	for _, port := range ports {
		for _, permanent := range []bool{false, true} {
			args := []string{}
			if permanent {
				args = append(args, "--permanent")
			}
			args = append(args, "--add-port="+strconv.Itoa(port)+"/tcp")
			if _, err := runPlusDeploymentCommand(ctx, "firewall-cmd", args, nil); err != nil {
				return errors.New("application firewall ports could not be opened")
			}
		}
	}
	return nil
}

func runPlusDeployment(ctx context.Context, p plusDeploymentPaths) (err error) {
	lock, err := plusPreparationLock(p.Preparation)
	if err != nil {
		return err
	}
	defer lock.Close()
	status := plusDeploymentStatus{State: "running", Stage: "validate", Message: "Checking Docker and setup choices.", Started: true}
	step := func(stage, message string) error {
		status.Stage = stage
		status.Message = message
		return savePlusDeploymentStatus(p, status)
	}
	defer func() {
		if err != nil {
			status.State = "failed"
			status.Message = "Setup stopped at " + status.Stage + ". Existing data is retained. Check the host setup logs and retry after fixing the cause."
			_ = savePlusDeploymentStatus(p, status)
		}
	}()
	if _, exists := os.Stat(filepath.Join(p.Configuration, "deployed")); exists == nil {
		return errors.New("this appliance is already deployed")
	}
	if err = step("validate", "Checking Docker and setup choices."); err != nil {
		return err
	}
	var request plusSetupRequest
	if err = readPlusJSON(filepath.Join(p.Preparation, "setup.json"), &request, true); err != nil {
		return errors.New("saved setup choices are unavailable")
	}
	mounts, e := readPlusSetupMounts(ctx)
	if e != nil {
		return errors.New("mounted storage is unavailable")
	}
	// Reuse the saved account name; validation must not replace it with root.
	validated := request
	validated.SeparateAccount = true
	if err = validatePlusSetup(&validated, request.Username, mounts, time.Now()); err != nil {
		return err
	}
	request = validated
	status.Components = request.Components
	if _, err = runPlusDeploymentCommand(ctx, "docker", []string{"info", "--format", "{{.ServerVersion}}"}, nil); err != nil {
		return errors.New("Docker Engine is not healthy")
	}
	if _, err = runPlusDeploymentCommand(ctx, "docker", []string{"compose", "version"}, nil); err != nil {
		return errors.New("Docker Compose is unavailable")
	}
	if err = step("configuration", "Preparing persistent directories and application configuration."); err != nil {
		return err
	}
	if err = preparePlusRuntime(p, request); err != nil {
		return err
	}
	if _, err = plusCompose(ctx, p, nil, "config", "--quiet"); err != nil {
		return errors.New("runtime Compose configuration is invalid")
	}
	if err = step("pull", "Pulling the selected upstream images."); err != nil {
		return err
	}
	if _, err = plusCompose(ctx, p, nil, "pull"); err != nil {
		return errors.New("an upstream image could not be pulled")
	}
	if err = step("database", "Starting selected database and cache services."); err != nil {
		return err
	}
	for _, service := range []struct {
		name    string
		enabled bool
	}{{"database", request.Components.Database}, {"cache", request.Components.Cache}} {
		if service.enabled {
			if _, err = plusCompose(ctx, p, nil, "up", "--detach", service.name); err != nil {
				return errors.New("database or cache startup failed")
			}
			if err = waitPlusContainer(ctx, p, service.name, true); err != nil {
				return err
			}
		}
	}
	if err = step("panel", "Starting Panel and creating its administrator and local node."); err != nil {
		return err
	}
	var panel plusPanelBootstrapResult
	if request.Components.Panel {
		if _, err = plusCompose(ctx, p, nil, "up", "--detach", "panel"); err != nil {
			return errors.New("Panel startup failed")
		}
		port := 8081
		if request.UseTLS {
			port = 8443
		}
		if err = waitPlusCondition(ctx, 8*time.Minute, func(ctx context.Context) bool { return checkPlusHTTP(ctx, request, port, "/auth/login") }); err != nil {
			return errors.New("Panel did not become reachable; partial selections may require external database or cache configuration")
		}
		panel, err = bootstrapPlusPanel(ctx, p, request)
		if err != nil {
			return err
		}
		status.PanelURL = plusApplicationURL(request.Host, request.UseTLS, port)
	}
	if err = step("applications", "Starting selected Wings and Drydock services."); err != nil {
		return err
	}
	if request.Components.Wings {
		if !request.Components.Panel {
			if _, e := os.Stat(filepath.Join(p.Configuration, "wings/config.yml")); e != nil {
				return errors.New("standalone Wings needs an administrator-supplied node configuration")
			}
		}
		if _, err = plusCompose(ctx, p, nil, "up", "--detach", "wings"); err != nil {
			return errors.New("Wings startup failed")
		}
		if err = waitPlusContainer(ctx, p, "wings", false); err != nil {
			return err
		}
	}
	if request.Components.Drydock {
		if _, err = plusCompose(ctx, p, nil, "up", "--detach", "drydock"); err != nil {
			return errors.New("Drydock startup failed")
		}
		if err = waitPlusCondition(ctx, 6*time.Minute, func(ctx context.Context) bool { return checkPlusHTTP(ctx, request, 3000, "/health") }); err != nil {
			return errors.New("Drydock did not become reachable")
		}
		status.DrydockURL = plusApplicationURL(request.Host, request.UseTLS, 3000)
	}
	if err = step("verify", "Checking the selected services and Panel-to-Wings connection."); err != nil {
		return err
	}
	for _, service := range plusSelectedServices(request.Components) {
		healthy := service == "database" || service == "cache" || service == "drydock"
		if err = waitPlusContainer(ctx, p, service, healthy); err != nil {
			return err
		}
	}
	if request.Components.Panel && request.Components.Wings {
		if err = waitPlusCondition(ctx, 3*time.Minute, func(ctx context.Context) bool { return verifyPlusPanelWings(ctx, p, panel.NodeID) }); err != nil {
			return errors.New("Panel could not authenticate to Wings")
		}
	}
	if err = activatePlusFirewall(ctx, request.Components, request.UseTLS); err != nil {
		return err
	}
	if err = step("finish", "Enabling stack startup and removing temporary setup credentials."); err != nil {
		return err
	}
	status.State = "succeeded"
	status.Stage = "complete"
	status.Message = "Setup complete. Open the selected applications from the desktop."
	public, err := json.Marshal(status)
	if err != nil {
		return err
	}
	if err = writePlusFile(filepath.Join(p.Configuration, "deployed"), public, 0644, -1, -1); err != nil {
		return err
	}
	if _, err = runPlusDeploymentCommand(ctx, "systemctl", []string{"daemon-reload"}, nil); err != nil {
		_ = os.Remove(filepath.Join(p.Configuration, "deployed"))
		return errors.New("stack storage dependency could not be loaded")
	}
	if _, err = runPlusDeploymentCommand(ctx, "systemctl", []string{"enable", "--now", "justvoxel-plus-stack.service"}, nil); err != nil {
		_ = os.Remove(filepath.Join(p.Configuration, "deployed"))
		return errors.New("stack startup could not be enabled")
	}
	if err = os.Remove(filepath.Join(p.Preparation, "setup.json")); err != nil {
		return errors.New("temporary setup credentials could not be removed")
	}
	if err = savePlusDeploymentStatus(p, status); err != nil {
		return err
	}
	return nil
}

func preparePlusWingsNetwork(ctx context.Context) (map[string]any, error) {
	const name = "justvoxel-plus-games"
	data, err := runPlusDeploymentCommand(ctx, "docker", []string{"network", "inspect", name}, nil)
	if err != nil {
		if _, err := runPlusDeploymentCommand(ctx, "docker", []string{"network", "create", "--driver", "bridge", name}, nil); err != nil {
			return nil, errors.New("Wings game network could not be created")
		}
		data, err = runPlusDeploymentCommand(ctx, "docker", []string{"network", "inspect", name}, nil)
		if err != nil {
			return nil, errors.New("Wings game network is unavailable")
		}
	}
	var networks []struct {
		Driver string
		IPAM   struct {
			Config []struct {
				Subnet  string
				Gateway string
			}
		}
	}
	if err := json.Unmarshal(data, &networks); err != nil || len(networks) != 1 || networks[0].Driver != "bridge" || len(networks[0].IPAM.Config) == 0 {
		return nil, errors.New("Wings game network is not a usable bridge")
	}
	ipam := networks[0].IPAM.Config[0]
	if net.ParseIP(ipam.Gateway) == nil {
		return nil, errors.New("Wings network gateway is unavailable")
	}
	// Let Docker allocate a free subnet. Do not collide with Wings' fixed default
	// subnet or implement a separate network allocator or game lifecycle.
	return map[string]any{"name": name, "network_mode": name, "interface": ipam.Gateway, "interfaces": map[string]any{"v4": map[string]string{"subnet": ipam.Subnet, "gateway": ipam.Gateway}}}, nil
}
