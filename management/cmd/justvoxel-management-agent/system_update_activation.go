package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
)

// Only the update reboot worker writes an expectation, immediately before reboot.
// Boot identity prevents a countdown, cancelled reboot, or agent restart from
// being mistaken for an activation failure.
type systemUpdateActivation struct {
	Version         int       `json:"version"`
	Expected        string    `json:"expected_digest"`
	Previous        string    `json:"previous_digest"`
	BootID          string    `json:"boot_id"`
	RequestedAt     time.Time `json:"requested_at"`
	FailureLog      string    `json:"failure_log"`
	SuccessLog      string    `json:"success_log"`
	FinalizedBootID string    `json:"finalized_boot_id,omitempty"`
}

var readSystemUpdateBootID = func() (string, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(data)), err
}
var bootIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var ostreeDeploymentLine = regexp.MustCompile(`^([* ]) ([^ ]+) ([a-f0-9]{64})\.([0-9]+)((?: \([a-z -]+\))*)$`)
var runUpdateOstreeStatus = func(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ostree", "admin", "status")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	return cmd.Output()
}

// No caller can supply command arguments. Streams remain separate in evidence.
var runSystemUpdateFinalize = func(ctx context.Context) ([]byte, []byte, int, error) {
	cmd := exec.CommandContext(ctx, "ostree", "admin", "finalize-staged", "--verbose")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		code = -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
	}
	return stdout.Bytes(), stderr.Bytes(), code, err
}

type bootcOstreeIdentity struct {
	Stateroot string `json:"stateroot"`
	Checksum  string `json:"checksum"`
	Serial    uint32 `json:"deploySerial"`
}

func (s *operationStore) activationLock() (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(s.baseDir, "system-update-activation.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
func (s *operationStore) activationPath() string {
	return filepath.Join(s.baseDir, "system-update-activation.json")
}
func (s *operationStore) readActivation() (*systemUpdateActivation, error) {
	f, err := os.OpenFile(s.activationPath(), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	owner, owned := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Size() > 4096 || info.Mode().Perm() != 0600 || !owned || owner.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("unsafe activation evidence")
	}
	var value systemUpdateActivation
	dec := json.NewDecoder(io.LimitReader(f, 4097))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if dec.Decode(&extra) != io.EOF || !validUpdateActivation(value) {
		return nil, errors.New("invalid activation evidence")
	}
	return &value, nil
}
func validUpdateActivation(value systemUpdateActivation) bool {
	return !(value.Version != 1 || !operationFingerprintPattern.MatchString(value.Expected) || !operationFingerprintPattern.MatchString(value.Previous) || value.Expected == value.Previous || !bootIDPattern.MatchString(value.BootID) || !validOperationID(value.FailureLog) || !validOperationID(value.SuccessLog) || value.FailureLog == value.SuccessLog || value.RequestedAt.IsZero() || (!bootIDPattern.MatchString(value.FinalizedBootID) && value.FinalizedBootID != ""))
}
func (s *operationStore) saveActivation(value systemUpdateActivation) error {
	if !validUpdateActivation(value) {
		return errors.New("invalid activation evidence")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err = atomicWrite(s.activationPath(), data, 0600); err != nil {
		return err
	}
	return syncSystemUpdateDirectory(s.baseDir)
}
func syncSystemUpdateDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func prepareSystemUpdateExpectation(ctx context.Context, digest string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	status, err := collectAdminSystemUpdateStatus(ctx)
	if err != nil {
		return err
	}
	store := &operationStore{baseDir: operationStateDir}
	return store.expectUpdateActivation(status, digest)
}

func (store *operationStore) expectUpdateActivation(status adminSystemUpdateStatus, digest string) error {
	status = expectedQueuedSystemUpdate(status, digest)
	if status.ReadOnly || !status.RebootRequired || status.Staged == nil || status.Staged.Digest != digest || !operationFingerprintPattern.MatchString(digest) || !operationFingerprintPattern.MatchString(status.Running.Digest) || digest == status.Running.Digest || status.stagedDownloadOnly {
		return errors.New("staged update changed before reboot")
	}
	bootID, err := readSystemUpdateBootID()
	if err != nil || !bootIDPattern.MatchString(bootID) {
		return errors.New("boot identity unavailable")
	}
	if err := ensurePrivateDirectory(store.baseDir); err != nil {
		return err
	}
	lock, err := store.activationLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	// Preserve corrupt evidence for diagnosis instead of replacing it silently.
	if _, err := store.readActivation(); err != nil {
		return err
	}
	failure, err := newOperationID()
	if err != nil {
		return err
	}
	success, err := newOperationID()
	if err != nil {
		return err
	}
	return store.saveActivation(systemUpdateActivation{Version: 1, Expected: digest, Previous: status.Running.Digest, BootID: bootID, RequestedAt: time.Now().UTC(), FailureLog: failure, SuccessLog: success})
}

func (s *operationStore) updateLog(id, text string) error {
	// Deterministic event IDs make refresh/restart retries idempotent, including
	// a crash after the log is synced but before the expectation is cleared.
	if _, err := s.readDiagnosticLog("update", id); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var clean strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if supportSensitivePattern.MatchString(line) {
			line = "<sensitive diagnostic detail redacted>"
		} else {
			line = strings.Map(func(r rune) rune {
				if unicode.IsControl(r) {
					return ' '
				}
				return r
			}, strings.ToValidUTF8(line, "�"))
			line = sanitizeSetupDiagnosticText(line, s.diagnosticHostname)
		}
		clean.WriteString(line)
		clean.WriteByte('\n')
	}
	_, err := s.storeDiagnosticReport("update", id, []byte(clean.String()))
	return err
}

// Finalizing an OSTree deployment can move it from bootc's staged slot to
// rollback with rollbackQueued=true. Use that slot only for the persisted,
// expected update; normal status and unrelated rollback UX stay unchanged.
func expectedQueuedSystemUpdate(status adminSystemUpdateStatus, digest string) adminSystemUpdateStatus {
	if status.Staged == nil && status.queued != nil && status.queued.Image.ImageDigest == digest && !status.queued.DownloadOnly && status.queued.Ostree != nil {
		status.Staged = bootcDeploymentForAPI(status.queued)
		status.stagedOstree = status.queued.Ostree
		status.stagedDownloadOnly = false
		status.RebootRequired = status.Running.Digest != digest
	}
	return status
}

func updateStatusSummary(status adminSystemUpdateStatus) string {
	staged := "none"
	if status.Staged != nil {
		staged = status.Staged.Digest
	}
	queued := "none"
	if status.queued != nil {
		queued = status.queued.Image.ImageDigest
	}
	return fmt.Sprintf("booted=%s staged_or_expected_next=%s queued_next=%s download_only=%t read_only=%t", status.Running.Digest, staged, queued, status.stagedDownloadOnly, status.ReadOnly)
}

// Match the exact deployment identified by bootc, never an arbitrary journal line.
// Finalization is verified only if that same deployment becomes the first,
// non-booted, non-staged deployment in OSTree's authoritative deployment list.
func updateOstreeTarget(output []byte, identity *bootcOstreeIdentity) (staged, finalized bool) {
	if identity == nil || !operationFingerprintPattern.MatchString("sha256:"+identity.Checksum) {
		return
	}
	first := true
	matches := 0
	for _, line := range strings.Split(string(output), "\n") {
		m := ostreeDeploymentLine.FindStringSubmatch(line)
		if m == nil {
			// Metadata lines are indented four spaces. An unrecognized deployment
			// header must not make a later deployment appear first.
			if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "    ") {
				return false, false
			}
			continue
		}
		if m[2] == identity.Stateroot && m[3] == identity.Checksum && m[4] == strconv.FormatUint(uint64(identity.Serial), 10) {
			matches++
			staged = first && m[1] != "*" && m[5] == " (staged)"
			finalized = first && m[1] != "*" && (m[5] == "" || m[5] == " (pending)")
		}
		first = false
	}
	if matches != 1 {
		return false, false
	}
	return
}

func (s *server) collectSystemUpdateStatus(ctx context.Context) (adminSystemUpdateStatus, error) {
	status, err := collectAdminSystemUpdateStatus(ctx)
	if err != nil || s.operations == nil {
		return status, err
	}
	lock, err := s.operations.activationLock()
	if err != nil {
		return status, err
	}
	defer lock.Close()
	return s.operations.reconcileUpdateActivation(ctx, status)
}
func (s *operationStore) reconcileUpdateActivation(ctx context.Context, status adminSystemUpdateStatus) (adminSystemUpdateStatus, error) {
	value, err := s.readActivation()
	if err != nil {
		status.ActivationFailed = true
		status.RecoveryMessage = "Update activation evidence is unavailable. See Update logs or contact support."
		return status, nil
	}
	if value == nil {
		return status, nil
	}
	status.ExpectedDigest = value.Expected
	if !operationFingerprintPattern.MatchString(status.Running.Digest) {
		return status, errors.New("booted digest unavailable")
	}
	bootID, err := readSystemUpdateBootID()
	if err != nil || !bootIDPattern.MatchString(bootID) {
		return status, errors.New("boot identity unavailable")
	}
	if status.Running.Digest == value.Expected {
		if err := s.updateLog(value.SuccessLog, "Update activated successfully after reboot.\nexpected_digest="+value.Expected); err != nil {
			return status, err
		}
		if err := os.Remove(s.activationPath()); err != nil {
			return status, err
		}
		status.ExpectedDigest = ""
		return status, syncSystemUpdateDirectory(s.baseDir)
	}
	status.ActivationPending = true
	if bootID == value.BootID {
		return status, nil
	}
	status.ActivationFailed = true
	status = expectedQueuedSystemUpdate(status, value.Expected)
	status.RecoveryStatus = "unavailable"
	status.RecoveryMessage = "The staged image did not activate on the previous reboot. Recovery cannot currently be verified. See Update logs."
	ostree, ostreeErr := runUpdateOstreeStatus(ctx)
	reason := "expected deployment is no longer staged or eligible for recovery"
	if status.Staged != nil && status.Staged.Digest == value.Expected && !status.ReadOnly && !status.stagedDownloadOnly {
		staged, finalized := updateOstreeTarget(ostree, status.stagedOstree)
		status.ActivationRecoverable = ostreeErr == nil && staged
		reason = "expected deployment finalization state could not be verified"
		if status.ActivationRecoverable {
			reason = "expected image remained staged after reboot"
			status.RecoveryStatus = "available"
			status.RecoveryMessage = "The staged image did not activate on the previous reboot. Force update can prepare it for another reboot."
		} else if ostreeErr == nil && finalized && value.FinalizedBootID == bootID {
			reason = "finalization verified; explicit reboot required"
			status.RecoveryStatus = "finalized"
			status.RecoveryMessage = "Update recovery is verified. Press Reboot into update to activate it."
		}
	}
	evidence := fmt.Sprintf("Update activation failed: expected image did not boot.\nrequested_at=%s\nold_running_digest=%s\nexpected_digest=%s\nactual_booted_digest=%s\nrecoverable=%t\nstate=%s\nreason=%s\nostree_status_available=%t\nbootc: %s\nostree status:\n%s", value.RequestedAt.Format(time.RFC3339), value.Previous, value.Expected, status.Running.Digest, status.ActivationRecoverable, status.RecoveryStatus, reason, ostreeErr == nil, updateStatusSummary(status), ostree)
	if err := s.updateLog(value.FailureLog, evidence); err != nil {
		return status, err
	}
	return status, nil
}

func (s *server) adminSystemUpdateForce(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if r.ContentLength != 0 || r.URL.RawQuery != "" {
		writeError(w, 400, "Force update takes no input")
		return
	}
	if s.operations == nil {
		writeError(w, 503, "Update recovery is unavailable")
		return
	}
	if !s.systemUpdateMu.TryLock() {
		writeError(w, 409, "A system update operation is already in progress")
		return
	}
	defer s.systemUpdateMu.Unlock()
	updateLock, err := acquireSystemUpdateLock()
	if err != nil {
		writeError(w, 409, "A system update reboot is in progress")
		return
	}
	defer updateLock.Close()
	lock, err := s.operations.activationLock()
	if err != nil {
		writeError(w, 409, "Update recovery is busy")
		return
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	before, err := collectAdminSystemUpdateStatus(ctx)
	if err == nil {
		before, err = s.operations.reconcileUpdateActivation(ctx, before)
	}
	if err != nil || !before.ActivationRecoverable {
		writeError(w, 409, "No recoverable update activation failure is verified")
		return
	}
	value, err := s.operations.readActivation()
	if err != nil || value == nil {
		writeError(w, 409, "Update recovery evidence is unavailable")
		return
	}
	id, err := newOperationID()
	if err != nil {
		writeError(w, 503, "Update recovery is unavailable")
		return
	}
	beforeOstree, err := runUpdateOstreeStatus(ctx)
	staged, _ := updateOstreeTarget(beforeOstree, before.stagedOstree)
	if err != nil || !staged {
		writeError(w, 409, "The staged deployment changed. Refresh and try again.")
		return
	}
	if err := s.operations.updateLog(id, "Force update requested: finalize the verified staged deployment.\n"+updateStatusSummary(before)); err != nil {
		writeError(w, 503, "Update recovery evidence could not be saved")
		return
	}
	stdout, stderr, exitCode, runErr := runSystemUpdateFinalize(ctx)
	// Use a fresh bounded context even if the command timed out or client left.
	afterCtx, afterCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer afterCancel()
	after, afterErr := collectAdminSystemUpdateStatus(afterCtx)
	after = expectedQueuedSystemUpdate(after, value.Expected)
	afterOstree, ostreeErr := runUpdateOstreeStatus(afterCtx)
	_, finalized := updateOstreeTarget(afterOstree, before.stagedOstree)
	sameTarget := after.Staged != nil && after.Staged.Digest == value.Expected && after.stagedOstree != nil && before.stagedOstree != nil && *after.stagedOstree == *before.stagedOstree
	verified := runErr == nil && exitCode == 0 && afterErr == nil && ostreeErr == nil && !after.ReadOnly && !after.stagedDownloadOnly && after.Running.Digest == before.Running.Digest && sameTarget && finalized
	report := fmt.Sprintf("Force update: finalize the verified staged deployment; no reboot requested.\nexit_code=%d\ncommand_completed=%t\nfinalization_verified=%t\nbefore bootc: %s\nbefore ostree:\n%s\nstdout:\n%s\nstderr:\n%s\nafter bootc: %s\nafter ostree:\n%s\nbootc_status_available=%t\nostree_status_available=%t\n", exitCode, runErr == nil, verified, updateStatusSummary(before), beforeOstree, stdout, stderr, updateStatusSummary(after), afterOstree, afterErr == nil, ostreeErr == nil)
	// A separate immutable report retains both the request and completed evidence.
	resultID, logErr := newOperationID()
	if logErr == nil {
		logErr = s.operations.updateLog(resultID, report)
	}
	if logErr != nil {
		writeError(w, 503, "Recovery ran, but its diagnostic log could not be saved. Refresh before continuing.")
		return
	}
	if verified {
		value.FinalizedBootID, err = readSystemUpdateBootID()
		if err == nil {
			err = s.operations.saveActivation(*value)
		}
		if err != nil {
			writeError(w, 503, "Recovery ran, but verification could not be saved. See Update logs.")
			return
		}
	}
	if afterErr == nil {
		after, afterErr = s.operations.reconcileUpdateActivation(afterCtx, after)
	}
	if !verified || afterErr != nil {
		writeError(w, 503, "Update recovery could not be verified. See Update logs and refresh before retrying.")
		return
	}
	writeJSON(w, 200, after)
}
