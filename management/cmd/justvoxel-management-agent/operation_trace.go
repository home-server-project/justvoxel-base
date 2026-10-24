package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"
)

const operationTraceLimit = 1024 * 1024
const operationTraceLineLimit = 16 * 1024

type operationTraceKey struct{}
type operationTraceContext struct {
	store   *operationStore
	id      string
	secrets []string
}

func withOperationTrace(ctx context.Context, store *operationStore, id string, secrets ...string) context.Context {
	return context.WithValue(ctx, operationTraceKey{}, operationTraceContext{store, id, secrets})
}
func supportedOperationTrace(kind string) bool {
	return kind == operationTypeMinecraftContainerUpdate || kind == operationTypeSetup || kind == operationTypeMinecraftReset || kind == operationTypeFactoryReset
}

var traceANSI = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[@-_])`)
var traceEscapedANSI = regexp.MustCompile(`\\(?:x1[bB]|u001[bB]|033)\[[0-?]*[ -/]*[@-~]`)
var traceSecret = regexp.MustCompile(`(?i)((?:--)?(?:[a-z_]*password|passwd|[a-z_]*token|cookie|authorization|credentials?|secret|api[_-]?key)["']?\s*[=:]\s*|--(?:password|token|credentials|secret)\s+)(?:\[redacted\]|"[^"\n]*"|'[^'\n]*'|[^\n,;]+)`)
var traceHeader = regexp.MustCompile(`(?i)(authorization|cookie|set-cookie)\s*:\s*.*`)
var traceURLCredential = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/\s@]+@`)

func sanitizeOperationTrace(text string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	text = traceANSI.ReplaceAllString(text, "")
	text = traceEscapedANSI.ReplaceAllString(text, "")
	text = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	text = traceHeader.ReplaceAllString(text, "$1: [redacted]")
	text = traceSecret.ReplaceAllString(text, "${1}[redacted]")
	return traceURLCredential.ReplaceAllString(text, "${1}[redacted]@")
}

// Walk the protected hierarchy through directory descriptors: never follow a
// replaced directory or trace symlink, and never accept a caller's destination.
func (s *operationStore) openTrace(id string, write bool) (*os.File, error) {
	if !validOperationID(id) {
		return nil, errOperationNotFound
	}
	op, err := s.get(id)
	if err != nil || !supportedOperationTrace(op.OperationType) {
		return nil, errOperationNotFound
	}
	fd, err := syscall.Open(s.baseDir, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	dir := os.NewFile(uintptr(fd), "management")
	defer func() { dir.Close() }()
	for _, name := range []string{"logs", "live-traces"} {
		info, e := dir.Stat()
		if e != nil || !privateTraceInfo(info, true) {
			return nil, errors.New("unsafe trace directory")
		}
		if write && name == "live-traces" {
			if e = syscall.Mkdirat(int(dir.Fd()), name, 0700); e != nil && e != syscall.EEXIST {
				return nil, e
			}
		}
		next, e := syscall.Openat(int(dir.Fd()), name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if e != nil {
			return nil, e
		}
		dir.Close()
		dir = os.NewFile(uintptr(next), name)
	}
	info, err := dir.Stat()
	if err != nil || !privateTraceInfo(info, true) {
		return nil, errors.New("unsafe trace directory")
	}
	flags := syscall.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_CLOEXEC | syscall.O_NONBLOCK
	if write {
		flags = syscall.O_WRONLY | syscall.O_APPEND | syscall.O_CREAT | syscall.O_NOFOLLOW | syscall.O_CLOEXEC | syscall.O_NONBLOCK
	}
	fd, err = syscall.Openat(int(dir.Fd()), id+".trace", flags, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), id+".trace")
	info, err = file.Stat()
	if err != nil || !privateTraceInfo(info, false) {
		file.Close()
		return nil, errors.New("unsafe trace file")
	}
	return file, nil
}
func privateTraceInfo(info os.FileInfo, directory bool) bool {
	if info == nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	mode := os.FileMode(0600)
	if directory {
		mode = 0700
	}
	return ok && stat.Uid == uint32(os.Geteuid()) && info.Mode().Perm() == mode &&
		((directory && info.IsDir()) || (!directory && info.Mode().IsRegular() && stat.Nlink == 1))
}
func (s *operationStore) appendTrace(id, record string, secrets []string) error {
	record = sanitizeOperationTrace(record, secrets)
	s.traceMu.Lock()
	defer s.traceMu.Unlock()
	file, err := s.openTrace(id, true)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	remaining := operationTraceLimit - int(info.Size())
	for _, line := range strings.Split(strings.TrimRight(record, "\n"), "\n") {
		// JSON responses/evidence are never the execution transparency surface.
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			continue
		}
		prefix := "[" + s.now().UTC().Format("15:04:05") + "] "
		if len(line)+len(prefix)+1 > operationTraceLineLimit {
			line = strings.ToValidUTF8(line[:operationTraceLineLimit-len(prefix)-20], "") + " [truncated]"
		}
		line = prefix + line + "\n"
		if len(line) > remaining {
			break
		}
		if _, err = io.WriteString(file, line); err != nil {
			return err
		}
		remaining -= len(line)
	}
	return nil
}
func (s *operationStore) readTrace(id string) ([]byte, error) {
	s.traceMu.Lock()
	defer s.traceMu.Unlock()
	file, err := s.openTrace(id, false)
	if errors.Is(err, syscall.ENOENT) {
		return []byte{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, operationTraceLimit))
	return data, err
}

// Drain oversized records without retaining fragments (a fragment could contain
// a secret whose identifying key was in an earlier fragment).
func (trace operationTraceContext) ingest(reader io.Reader) {
	input := bufio.NewReaderSize(reader, operationTraceLineLimit)
	oversized := false
	failed := false
	for {
		line, err := input.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			oversized = true
			continue
		}
		if !failed && !oversized && len(line) > 0 {
			if err := trace.store.appendTrace(trace.id, string(line), trace.secrets); err != nil {
				log.Printf("operation %s execution trace is unavailable", trace.id)
				failed = true
			}
		}
		oversized = false
		if err != nil {
			return
		}
	}
}

// The JSON stdout/stderr boundary is untouched. Only inherited FD 3 is read.
func attachOperationTrace(ctx context.Context, cmd *exec.Cmd) (func(error), error) {
	trace, ok := ctx.Value(operationTraceKey{}).(operationTraceContext)
	if !ok {
		return func(error) {}, nil
	}
	if trace.store == nil {
		return func(error) {}, nil
	}
	if _, err := trace.store.get(trace.id); err != nil {
		return func(error) {}, nil
	}
	read, write, err := os.Pipe()
	if err != nil {
		log.Printf("operation %s execution trace is unavailable", trace.id)
		return func(error) {}, nil
	}
	cmd.ExtraFiles = []*os.File{write}
	originalEnv := cmd.Env
	env := originalEnv
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, "JV_OPERATION_TRACE_FD=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "JV_OPERATION_TRACE_FD=3")
	command := "$ " + cmd.Path
	hideNext := false
	for _, arg := range cmd.Args[1:] {
		if hideNext {
			arg = "[redacted]"
			hideNext = false
		} else {
			switch strings.ToLower(arg) {
			case "--password", "--token", "--credentials", "--secret", "--authorization", "--cookie":
				hideNext = true
			}
		}
		if strings.ContainsAny(arg, " \t\r\n\"'") {
			command += " " + fmt.Sprintf("%q", arg)
		} else {
			command += " " + arg
		}
	}
	if err = trace.store.appendTrace(trace.id, command, trace.secrets); err != nil {
		read.Close()
		write.Close()
		cmd.ExtraFiles = nil
		cmd.Env = originalEnv
		log.Printf("operation %s execution trace is unavailable", trace.id)
		return func(error) {}, nil
	}
	done := make(chan struct{})
	go func() { defer close(done); trace.ingest(read) }()
	return func(runErr error) {
		write.Close()
		// An inherited descriptor held by a stale descendant must not block the worker.
		select {
		case <-done:
		case <-time.After(time.Second):
			read.Close()
			<-done
		}
		read.Close()
		code := 0
		if runErr != nil {
			code = -1
			var exit *exec.ExitError
			if errors.As(runErr, &exit) {
				code = exit.ExitCode()
			}
		}
		_ = trace.store.appendTrace(trace.id, fmt.Sprintf("[exit %d]", code), trace.secrets)
	}, nil
}
func traceStage(ctx context.Context, stage string) {
	if trace, ok := ctx.Value(operationTraceKey{}).(operationTraceContext); ok {
		_ = trace.store.appendTrace(trace.id, "STAGE "+stage, trace.secrets)
	}
}
func (s *server) adminOperationTrace(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	id := r.PathValue("id")
	if !validOperationID(id) {
		writeError(w, 400, "invalid operation id")
		return
	}
	if s.operations == nil {
		writeError(w, 503, "operation trace is unavailable")
		return
	}
	op, err := s.operations.get(id)
	if err != nil || !supportedOperationTrace(op.OperationType) {
		writeError(w, 404, "operation trace not found")
		return
	}
	if isServerMigrationOperation(op.OperationType) && (migrationInstanceID(r.Context()) == "" || migrationInstanceID(r.Context()) != op.InstanceID) {
		writeError(w, 404, "migration operation trace not found")
		return
	}
	data, err := s.operations.readTrace(id)
	if err != nil {
		writeError(w, 500, "operation trace could not be read")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}
