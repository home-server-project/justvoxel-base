package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"
)

const supportReportLimit = 256 * 1024
const supportSectionLimit = 64 * 1024
const supportLineLimit = 2048
const supportInventoryLimit = 200

var supportSensitivePattern = regexp.MustCompile(`(?i)password|secret|token|cookie|authorization|credential|csrf|api[ _-]?key|environment|https?://[^\s]*@`)

// Keep stdout bounded while draining it; stderr and command errors never enter reports.
type supportCommandOutput struct {
	bytes.Buffer
	truncated bool
}

func (b *supportCommandOutput) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > supportSectionLimit-b.Len() {
		p = p[:supportSectionLimit-b.Len()]
		b.truncated = true
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

var runSupportCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.WaitDelay = time.Second
	var output supportCommandOutput
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return nil, err
	}
	if output.truncated {
		// Drop a partial final line: it could be the continuation of sensitive output.
		data := output.Bytes()
		end := bytes.LastIndexByte(data, '\n')
		if end < 0 {
			return []byte("<section truncated>\n"), nil
		}
		return append(append([]byte(nil), data[:end+1]...), []byte("<section truncated>\n")...), nil
	}
	return output.Bytes(), nil
}

func (s *server) adminSupportReport(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	// This operation accepts no request capabilities, even if a caller sends a body.
	if r.URL.RawQuery != "" || r.ContentLength != 0 {
		writeError(w, http.StatusBadRequest, "support report takes no input")
		return
	}
	if s.operations == nil {
		writeError(w, http.StatusServiceUnavailable, "support report is unavailable")
		return
	}
	entry, err := s.operations.generateSupportReport(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "support report could not be generated")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "log": entry})
}

func sanitizeSupportLine(line, hostname string) string {
	if supportSensitivePattern.MatchString(line) {
		return "<sensitive diagnostic detail redacted>"
	}
	line = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(line, "�"))
	line = sanitizeSetupDiagnosticText(line, hostname)
	if len(line) > supportLineLimit {
		return "<diagnostic line truncated>"
	}
	return line
}

type supportReport struct {
	strings.Builder
	hostname  string
	truncated bool
}

func (b *supportReport) section(title string, data []byte) {
	const marker = "<report truncated>\n"
	if b.truncated || b.Len() >= supportReportLimit-len(marker) {
		return
	}
	var text strings.Builder
	text.WriteString("\n" + title + "\n")
	// Bound even injected command output; sanitize complete lines before publication.
	if len(data) > supportSectionLimit {
		end := bytes.LastIndexByte(data[:supportSectionLimit], '\n')
		if end < 0 {
			data = []byte("<section truncated>\n")
		} else {
			data = append(append([]byte(nil), data[:end+1]...), []byte("<section truncated>\n")...)
		}
	}
	for _, line := range strings.Split(string(data), "\n") {
		safe := sanitizeSupportLine(line, b.hostname) + "\n"
		if text.Len()+len(safe) > supportSectionLimit-len("<section truncated>\n") {
			text.WriteString("<section truncated>\n")
			break
		}
		text.WriteString(safe)
	}
	if b.Len()+text.Len() > supportReportLimit-len(marker) {
		b.WriteString(marker)
		b.truncated = true
		// Never publish partial lines.
		return
	}
	b.WriteString(text.String())
}
func (b *supportReport) command(ctx context.Context, title, name string, args ...string) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	data, err := runSupportCommand(ctx, name, args...)
	if err != nil {
		data = []byte("<section unavailable>")
	}
	b.section(title, data)
}

func supportPathMetadata(label string, info os.FileInfo, err error) string {
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Sprintf("%q missing\n", label)
	}
	if err != nil {
		return fmt.Sprintf("%q <metadata unavailable>\n", label)
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Sprintf("%q <metadata unavailable>\n", label)
	}
	return fmt.Sprintf("%q exists type=%s mode=%04o uid=%d gid=%d size=%d modified=%s\n", label, info.Mode().Type().String(), info.Mode().Perm(), owner.Uid, owner.Gid, info.Size(), info.ModTime().UTC().Format(time.RFC3339))
}

// Each directory is opened relative to its already-open parent without following
// symlinks. Readdir is incremental so large directories cannot exhaust memory.
func supportInventory(base string) []byte {
	var out strings.Builder
	fd, err := syscall.Open(base, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return []byte("<section unavailable>")
	}
	root := os.NewFile(uintptr(fd), base)
	defer root.Close()
	count := 0
	var visit func(*os.File, string, int)
	visit = func(dir *os.File, prefix string, depth int) {
		for count < supportInventoryLimit {
			entries, err := dir.Readdir(1)
			if err != nil && err != io.EOF {
				out.WriteString("<inventory metadata unavailable>\n")
				return
			}
			if len(entries) == 0 {
				return
			}
			entry := entries[0]
			count++
			label := prefix + entry.Name()
			out.WriteString(supportPathMetadata(label, entry, nil))
			if depth < 2 && entry.IsDir() && entry.Mode()&os.ModeSymlink == 0 {
				childFD, err := syscall.Openat(int(dir.Fd()), entry.Name(), syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
				if err != nil {
					out.WriteString("<inventory directory unavailable>\n")
					continue
				}
				child := os.NewFile(uintptr(childFD), label)
				visit(child, label+"/", depth+1)
				child.Close()
			}
		}
	}
	visit(root, "", 1)
	if count >= supportInventoryLimit {
		out.WriteString("<inventory truncated: entry limit reached>\n")
	}
	return []byte(out.String())
}

func (s *operationStore) generateSupportReport(ctx context.Context) (diagnosticLogEntry, error) {
	id, err := newOperationID()
	if err != nil {
		return diagnosticLogEntry{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	var report supportReport
	report.hostname = s.diagnosticHostname
	meta, err := readReleaseMetadata()
	if err != nil {
		meta = releaseMetadata{Version: "unavailable", SourceCommit: "unavailable", ManagementAPI: "v1"}
	}
	report.section("REPORT METADATA", []byte(fmt.Sprintf("generated_at=%s\nwebui_version=%s\nsource_commit=%s\nmanagement_api=%s\n", s.now().UTC().Format(time.RFC3339Nano), meta.Version, meta.SourceCommit, meta.ManagementAPI)))
	for _, service := range []struct{ title, unit, lines string }{
		{"MANAGEMENT", "justvoxel-management.service", "300"},
		{"WEBUI", "justvoxel-webui.service", "150"},
	} {
		report.command(ctx, service.title+" SERVICE STATE", "systemctl", "show", service.unit, "--no-pager", "--property=ActiveState,SubState,Result,NRestarts,ExecMainCode,ExecMainStatus")
	}
	info, statErr := os.Lstat("/run/justvoxel/management.sock")
	report.section("MANAGEMENT SOCKET STATE", []byte(supportPathMetadata("/run/justvoxel/management.sock", info, statErr)))
	for _, service := range []struct{ title, unit, lines string }{
		{"MANAGEMENT", "justvoxel-management.service", "300"},
		{"WEBUI", "justvoxel-webui.service", "150"},
	} {
		// cat omits journal hostname prefixes; message host identities are sanitized below.
		report.command(ctx, "CURRENT-BOOT "+service.title+" JOURNAL", "journalctl", "-b", "-u", service.unit, "--no-pager", "-n", service.lines, "-o", "cat")
	}
	var paths strings.Builder
	for _, path := range []string{"/etc/justvoxel", "/etc/justvoxel/instances", "/var/lib/justvoxel/instances", "/var/lib/justvoxel/instances/names", "/var/lib/justvoxel/management", "/run/justvoxel", "/run/justvoxel/management.sock"} {
		info, err := os.Lstat(path)
		paths.WriteString(supportPathMetadata(path, info, err))
	}
	report.section("IMPORTANT APPLIANCE PATH STATE", []byte(paths.String()))
	report.section("MANAGEMENT STATE INVENTORY", supportInventory(s.baseDir))
	return s.storeSupportReport(id, []byte(report.String()))
}

func (s *operationStore) storeSupportReport(id string, data []byte) (diagnosticLogEntry, error) {
	return s.storeDiagnosticReport("support", id, data)
}

func (s *operationStore) storeDiagnosticReport(category, id string, data []byte) (diagnosticLogEntry, error) {
	if (category != "support" && category != "network") || !validOperationID(id) || len(data) > supportReportLimit {
		return diagnosticLogEntry{}, errors.New("invalid support report")
	}
	s.diagnosticMu.Lock()
	defer s.diagnosticMu.Unlock()
	if err := validatePrivateDiagnosticDirectory(s.logsDir); err != nil {
		return diagnosticLogEntry{}, err
	}
	dir := s.diagnosticCategoryDirectory(category)
	if err := validatePrivateDiagnosticDirectory(dir); err != nil {
		return diagnosticLogEntry{}, err
	}
	dirFD, err := syscall.Open(dir, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return diagnosticLogEntry{}, err
	}
	directory := os.NewFile(uintptr(dirFD), dir)
	defer directory.Close()
	// Validate the pinned directory too, before creating any file inside it.
	info, err := directory.Stat()
	if err != nil {
		return diagnosticLogEntry{}, err
	}
	owner, owned := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm() != 0700 || !owned || owner.Uid != uint32(os.Geteuid()) {
		return diagnosticLogEntry{}, errors.New("unsafe support directory")
	}
	name := id + ".log"
	fd, err := syscall.Openat(dirFD, name, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return diagnosticLogEntry{}, err
	}
	file := os.NewFile(uintptr(fd), name)
	complete := false
	defer func() {
		file.Close()
		if !complete {
			_ = syscall.Unlinkat(dirFD, name)
		}
	}()
	info, err = file.Stat()
	if err != nil {
		return diagnosticLogEntry{}, err
	}
	owner, owned = info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !owned || owner.Uid != uint32(os.Geteuid()) {
		return diagnosticLogEntry{}, errors.New("unsafe support file")
	}
	if _, err := file.Write(data); err != nil {
		return diagnosticLogEntry{}, err
	}
	if err := file.Sync(); err != nil {
		return diagnosticLogEntry{}, err
	}
	info, err = file.Stat()
	if err != nil {
		return diagnosticLogEntry{}, err
	}
	if err := file.Close(); err != nil {
		return diagnosticLogEntry{}, err
	}
	if err := directory.Sync(); err != nil {
		return diagnosticLogEntry{}, err
	}
	complete = true
	return diagnosticLogEntry{Category: category, LogID: id, Timestamp: info.ModTime().UTC().Format(time.RFC3339Nano)}, nil
}
