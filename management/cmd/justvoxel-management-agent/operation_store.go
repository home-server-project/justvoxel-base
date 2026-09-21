package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	operationSchemaVersion = "v1"
	operationStateDir      = "/var/lib/justvoxel/management"
	operationTypeSetup         = "setup"
	operationTypeRestore       = "restore"
	operationTypeDataMigration = "data_migration"
	operationTypeMigrationExport = "migration_export"
	operationTypeMigrationImport = "migration_import"
	operationTypeMigrationRecovery = "migration_recovery"
)

type operationState string

const (
	operationQueued         operationState = "queued"
	operationValidating     operationState = "validating"
	operationRunning        operationState = "running"
	operationVerifying      operationState = "verifying"
	operationSucceeded      operationState = "succeeded"
	operationFailed         operationState = "failed"
	operationRollingBack    operationState = "rolling_back"
	operationRolledBack     operationState = "rolled_back"
	operationNeedsAttention operationState = "needs_attention"
)

var (
	operationIDPattern          = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	operationFingerprintPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	operationStagePattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

	errOperationNotFound    = errors.New("operation not found")
	errSetupOperationBusy   = errors.New("another setup operation is already active")
	errSetupLockBusy        = errors.New("setup operation lock is already held")
	errRestoreOperationBusy      = errors.New("another restore operation is already active")
	errRestoreLockBusy           = errors.New("restore operation lock is already held")
	errDataMigrationOperationBusy = errors.New("another data migration operation is already active")
	errDataMigrationLockBusy      = errors.New("data migration operation lock is already held")
	errMigrationOperationBusy     = errors.New("another server migration operation is already active")
	errMigrationLockBusy          = errors.New("server migration operation lock is already held")
)

type operationRollback struct {
	State  string `json:"state"`
	Result string `json:"result,omitempty"`
}

type operationJournal struct {
	SchemaVersion   string            `json:"schema_version"`
	OperationID     string            `json:"operation_id"`
	OperationType   string            `json:"operation_type"`
	PlanFingerprint string            `json:"plan_fingerprint"`
	State           operationState    `json:"state"`
	Stage           string            `json:"stage"`
	Status          string            `json:"status"`
	StartedAt       string            `json:"started_at"`
	UpdatedAt       string            `json:"updated_at"`
	FinishedAt      string            `json:"finished_at,omitempty"`
	InterruptedAt   string            `json:"interrupted_at,omitempty"`
	Rollback        operationRollback `json:"rollback"`
}

type operationStore struct {
	mu               sync.Mutex
	baseDir          string
	operationsDir    string
	lockFile         *os.File
	restoreLockFile      *os.File
	dataMigrationLockFile *os.File
	migrationLockFile     *os.File
	setupLockHeld        bool
	restoreLockHeld      bool
	dataMigrationLockHeld bool
	migrationLockHeld     bool
	currentSetupID       string
	currentRestoreID     string
	currentDataMigrationID string
	currentMigrationID     string
	operations       map[string]operationJournal
	now              func() time.Time
}

func openOperationStore(baseDir string) (*operationStore, error) {
	if baseDir == "" {
		return nil, errors.New("operation state directory is required")
	}
	operationsDir := filepath.Join(baseDir, "operations")
	if err := ensurePrivateDirectory(baseDir); err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(operationsDir); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(baseDir, "setup.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open setup operation lock: %w", err)
	}
	if err := lockFile.Chmod(0o600); err != nil {
		lockFile.Close()
		return nil, fmt.Errorf("protect setup operation lock: %w", err)
	}
	restoreLockPath := filepath.Join(baseDir, "restore.lock")
	restoreLockFile, err := os.OpenFile(restoreLockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		lockFile.Close()
		return nil, fmt.Errorf("open restore operation lock: %w", err)
	}
	if err := restoreLockFile.Chmod(0o600); err != nil {
		restoreLockFile.Close()
		lockFile.Close()
		return nil, fmt.Errorf("protect restore operation lock: %w", err)
	}
	dataMigrationLockPath := filepath.Join(baseDir, "data-migration.lock")
	dataMigrationLockFile, err := os.OpenFile(dataMigrationLockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		restoreLockFile.Close()
		lockFile.Close()
		return nil, fmt.Errorf("open data migration operation lock: %w", err)
	}
	if err := dataMigrationLockFile.Chmod(0o600); err != nil {
		dataMigrationLockFile.Close()
		restoreLockFile.Close()
		lockFile.Close()
		return nil, fmt.Errorf("protect data migration operation lock: %w", err)
	}
	migrationLockPath := filepath.Join(baseDir, "server-migration.lock")
	migrationLockFile, err := os.OpenFile(migrationLockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		dataMigrationLockFile.Close()
		restoreLockFile.Close()
		lockFile.Close()
		return nil, fmt.Errorf("open server migration operation lock: %w", err)
	}
	if err := migrationLockFile.Chmod(0o600); err != nil {
		migrationLockFile.Close()
		dataMigrationLockFile.Close()
		restoreLockFile.Close()
		lockFile.Close()
		return nil, fmt.Errorf("protect server migration operation lock: %w", err)
	}

	s := &operationStore{
		baseDir:               baseDir,
		operationsDir:         operationsDir,
		lockFile:              lockFile,
		restoreLockFile:       restoreLockFile,
		dataMigrationLockFile: dataMigrationLockFile,
		migrationLockFile:     migrationLockFile,
		operations:            make(map[string]operationJournal),
		now:                   time.Now,
	}
	if err := s.loadAndRecover(); err != nil {
		migrationLockFile.Close()
		dataMigrationLockFile.Close()
		restoreLockFile.Close()
		lockFile.Close()
		return nil, err
	}
	return s, nil
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create operation state directory %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("protect operation state directory %s: %w", path, err)
	}
	return nil
}

func (s *operationStore) close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.setupLockHeld && s.lockFile != nil {
		_ = syscall.Flock(int(s.lockFile.Fd()), syscall.LOCK_UN)
		s.setupLockHeld = false
	}
	if s.restoreLockHeld && s.restoreLockFile != nil {
		_ = syscall.Flock(int(s.restoreLockFile.Fd()), syscall.LOCK_UN)
		s.restoreLockHeld = false
	}
	if s.dataMigrationLockHeld && s.dataMigrationLockFile != nil {
		_ = syscall.Flock(int(s.dataMigrationLockFile.Fd()), syscall.LOCK_UN)
		s.dataMigrationLockHeld = false
	}
	if s.migrationLockHeld && s.migrationLockFile != nil {
		_ = syscall.Flock(int(s.migrationLockFile.Fd()), syscall.LOCK_UN)
		s.migrationLockHeld = false
	}

	var firstErr error
	if s.migrationLockFile != nil {
		if err := s.migrationLockFile.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.migrationLockFile = nil
	}
	if s.dataMigrationLockFile != nil {
		if err := s.dataMigrationLockFile.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.dataMigrationLockFile = nil
	}
	if s.restoreLockFile != nil {
		if err := s.restoreLockFile.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.restoreLockFile = nil
	}
	if s.lockFile != nil {
		if err := s.lockFile.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.lockFile = nil
	}
	return firstErr
}

func (s *operationStore) loadAndRecover() error {
	entries, err := os.ReadDir(s.operationsDir)
	if err != nil {
		return fmt.Errorf("read operation journals: %w", err)
	}
	activeSetup := ""
	activeRestore := ""
	activeDataMigration := ""
	activeMigration := ""
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect operation journal %s: %w", entry.Name(), err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("operation journal %s is not a regular file", entry.Name())
		}
		path := filepath.Join(s.operationsDir, entry.Name())
		journal, err := readOperationJournal(path)
		if err != nil {
			return fmt.Errorf("read operation journal %s: %w", entry.Name(), err)
		}
		if entry.Name() != journal.OperationID+".json" {
			return fmt.Errorf("operation journal filename does not match operation id")
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("protect operation journal %s: %w", entry.Name(), err)
		}
		if operationIsCurrent(journal.State) {
			switch journal.OperationType {
			case operationTypeSetup:
				if activeSetup != "" {
					return errors.New("multiple active setup operation journals require attention")
				}
				activeSetup = journal.OperationID
			case operationTypeRestore:
				if activeRestore != "" {
					return errors.New("multiple active restore operation journals require attention")
				}
				activeRestore = journal.OperationID
			case operationTypeDataMigration:
				if activeDataMigration != "" {
					return errors.New("multiple active data migration operation journals require attention")
				}
				activeDataMigration = journal.OperationID
			case operationTypeMigrationExport, operationTypeMigrationImport, operationTypeMigrationRecovery:
				if activeMigration != "" {
					return errors.New("multiple active server migration operation journals require attention")
				}
				activeMigration = journal.OperationID
			}
		}
		s.operations[journal.OperationID] = journal
	}

	if activeSetup != "" {
		if err := s.acquireSetupLock(); err != nil {
			return err
		}
		journal := s.operations[activeSetup]
		if operationInterruptedByRestart(journal.State) {
			now := s.now().UTC().Format(time.RFC3339Nano)
			journal.State = operationNeedsAttention
			journal.Stage = "interrupted"
			journal.Status = "Setup was interrupted before completion."
			journal.UpdatedAt = now
			journal.InterruptedAt = now
			if err := s.persist(journal); err != nil {
				_ = s.releaseSetupLock()
				return err
			}
			s.operations[journal.OperationID] = journal
		}
		s.currentSetupID = activeSetup
	}

	if activeRestore != "" {
		if err := s.acquireRestoreLock(); err != nil {
			if activeSetup != "" {
				_ = s.releaseSetupLock()
			}
			return err
		}
		journal := s.operations[activeRestore]
		if operationInterruptedByRestart(journal.State) {
			now := s.now().UTC().Format(time.RFC3339Nano)
			journal.State = operationNeedsAttention
			journal.Stage = "interrupted"
			journal.Status = "Restore was interrupted before completion. Review preserved restore recovery state before continuing."
			journal.UpdatedAt = now
			journal.InterruptedAt = now
			if err := s.persist(journal); err != nil {
				_ = s.releaseRestoreLock()
				if activeSetup != "" {
					_ = s.releaseSetupLock()
				}
				return err
			}
			s.operations[journal.OperationID] = journal
		}
		s.currentRestoreID = activeRestore
	}

	if activeDataMigration != "" {
		if err := s.acquireDataMigrationLock(); err != nil {
			if activeRestore != "" {
				_ = s.releaseRestoreLock()
			}
			if activeSetup != "" {
				_ = s.releaseSetupLock()
			}
			return err
		}
		journal := s.operations[activeDataMigration]
		if operationInterruptedByRestart(journal.State) {
			now := s.now().UTC().Format(time.RFC3339Nano)
			journal.State = operationNeedsAttention
			journal.Stage = "interrupted"
			journal.Status = "Minecraft data migration was interrupted before completion. Review preserved migration state before continuing."
			journal.UpdatedAt = now
			journal.InterruptedAt = now
			if err := s.persist(journal); err != nil {
				_ = s.releaseDataMigrationLock()
				return err
			}
			s.operations[journal.OperationID] = journal
		}
		s.currentDataMigrationID = activeDataMigration
	}

	if activeMigration != "" {
		if err := s.acquireMigrationLock(); err != nil {
			if activeDataMigration != "" {
				_ = s.releaseDataMigrationLock()
			}
			if activeRestore != "" {
				_ = s.releaseRestoreLock()
			}
			if activeSetup != "" {
				_ = s.releaseSetupLock()
			}
			return err
		}
		journal := s.operations[activeMigration]
		if operationInterruptedByRestart(journal.State) {
			now := s.now().UTC().Format(time.RFC3339Nano)
			journal.State = operationNeedsAttention
			journal.Stage = "interrupted"
			if journal.OperationType == operationTypeMigrationImport {
				journal.Status = "Server migration import was interrupted before completion. Review preserved import recovery state and the Minecraft runtime before continuing."
			} else if journal.OperationType == operationTypeMigrationRecovery {
				journal.Status = "Server migration recovery finalization was interrupted. Review retained recovery state before continuing."
			} else {
				journal.Status = "Server migration export was interrupted before completion. Review the destination and Minecraft runtime before continuing."
			}
			journal.UpdatedAt = now
			journal.InterruptedAt = now
			if err := s.persist(journal); err != nil {
				_ = s.releaseMigrationLock()
				return err
			}
			s.operations[journal.OperationID] = journal
		}
		s.currentMigrationID = activeMigration
	}
	return nil
}

func readOperationJournal(path string) (operationJournal, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return operationJournal{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var journal operationJournal
	if err := decoder.Decode(&journal); err != nil {
		return operationJournal{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return operationJournal{}, errors.New("unexpected trailing JSON value")
		}
		return operationJournal{}, err
	}
	if err := validateOperationJournal(journal); err != nil {
		return operationJournal{}, err
	}
	return journal, nil
}

func validateOperationJournal(journal operationJournal) error {
	if journal.SchemaVersion != operationSchemaVersion {
		return errors.New("unsupported operation journal schema")
	}
	if !validOperationID(journal.OperationID) {
		return errors.New("invalid operation id")
	}
	if journal.OperationType != operationTypeSetup && journal.OperationType != operationTypeRestore && journal.OperationType != operationTypeDataMigration && journal.OperationType != operationTypeMigrationExport && journal.OperationType != operationTypeMigrationImport && journal.OperationType != operationTypeMigrationRecovery {
		return errors.New("unsupported operation type")
	}
	if !operationFingerprintPattern.MatchString(journal.PlanFingerprint) {
		return errors.New("invalid operation plan fingerprint")
	}
	if !validOperationState(journal.State) {
		return errors.New("invalid operation state")
	}
	if journal.Stage != "" && !operationStagePattern.MatchString(journal.Stage) {
		return errors.New("invalid operation stage")
	}
	if !validOperationStatus(journal.Status) {
		return errors.New("invalid operation status")
	}
	if journal.StartedAt == "" || journal.UpdatedAt == "" {
		return errors.New("operation timestamps are incomplete")
	}
	if journal.Rollback.State == "" {
		return errors.New("rollback state is required")
	}
	return nil
}

func validOperationID(value string) bool {
	return operationIDPattern.MatchString(value)
}

func validOperationState(state operationState) bool {
	switch state {
	case operationQueued, operationValidating, operationRunning, operationVerifying, operationSucceeded,
		operationFailed, operationRollingBack, operationRolledBack, operationNeedsAttention:
		return true
	default:
		return false
	}
}

func validOperationStatus(value string) bool {
	return value != "" && len(value) <= 512 && !strings.ContainsAny(value, "\r\n")
}

func operationIsCurrent(state operationState) bool {
	return state != operationSucceeded && state != operationRolledBack
}

func operationInterruptedByRestart(state operationState) bool {
	switch state {
	case operationQueued, operationValidating, operationRunning, operationVerifying, operationFailed, operationRollingBack:
		return true
	default:
		return false
	}
}

func (s *operationStore) beginSetup(planFingerprint string) (operationJournal, bool, error) {
	if !operationFingerprintPattern.MatchString(planFingerprint) {
		return operationJournal{}, false, errors.New("invalid setup plan fingerprint")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.currentSetupID != "" {
		current := s.operations[s.currentSetupID]
		if current.PlanFingerprint == planFingerprint {
			return current, false, nil
		}
		return operationJournal{}, false, errSetupOperationBusy
	}
	if err := s.acquireSetupLock(); err != nil {
		return operationJournal{}, false, err
	}

	id, err := newOperationID()
	if err != nil {
		_ = s.releaseSetupLock()
		return operationJournal{}, false, err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	journal := operationJournal{
		SchemaVersion:   operationSchemaVersion,
		OperationID:     id,
		OperationType:   operationTypeSetup,
		PlanFingerprint: planFingerprint,
		State:           operationQueued,
		Stage:           "queued",
		Status:          "Setup operation queued.",
		StartedAt:       now,
		UpdatedAt:       now,
		Rollback:        operationRollback{State: "not_started"},
	}
	if err := s.persist(journal); err != nil {
		_ = s.releaseSetupLock()
		return operationJournal{}, false, err
	}
	s.operations[id] = journal
	s.currentSetupID = id
	return journal, true, nil
}


func (s *operationStore) beginRestore(planFingerprint string) (operationJournal, bool, error) {
	if !operationFingerprintPattern.MatchString(planFingerprint) {
		return operationJournal{}, false, errors.New("invalid restore plan fingerprint")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.currentRestoreID != "" {
		current := s.operations[s.currentRestoreID]
		if current.PlanFingerprint == planFingerprint {
			return current, false, nil
		}
		return operationJournal{}, false, errRestoreOperationBusy
	}
	if err := s.acquireRestoreLock(); err != nil {
		return operationJournal{}, false, err
	}

	id, err := newOperationID()
	if err != nil {
		_ = s.releaseRestoreLock()
		return operationJournal{}, false, err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	journal := operationJournal{
		SchemaVersion:   operationSchemaVersion,
		OperationID:     id,
		OperationType:   operationTypeRestore,
		PlanFingerprint: planFingerprint,
		State:           operationQueued,
		Stage:           "queued",
		Status:          "Restore operation queued.",
		StartedAt:       now,
		UpdatedAt:       now,
		Rollback:        operationRollback{State: "not_started"},
	}
	if err := s.persist(journal); err != nil {
		_ = s.releaseRestoreLock()
		return operationJournal{}, false, err
	}
	s.operations[id] = journal
	s.currentRestoreID = id
	return journal, true, nil
}


func (s *operationStore) beginDataMigration(planFingerprint string) (operationJournal, bool, error) {
	if !operationFingerprintPattern.MatchString(planFingerprint) {
		return operationJournal{}, false, errors.New("invalid data migration plan fingerprint")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.currentDataMigrationID != "" {
		current := s.operations[s.currentDataMigrationID]
		if current.PlanFingerprint == planFingerprint {
			return current, false, nil
		}
		return operationJournal{}, false, errDataMigrationOperationBusy
	}
	if err := s.acquireDataMigrationLock(); err != nil {
		return operationJournal{}, false, err
	}

	id, err := newOperationID()
	if err != nil {
		_ = s.releaseDataMigrationLock()
		return operationJournal{}, false, err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	journal := operationJournal{
		SchemaVersion:   operationSchemaVersion,
		OperationID:     id,
		OperationType:   operationTypeDataMigration,
		PlanFingerprint: planFingerprint,
		State:           operationQueued,
		Stage:           "queued",
		Status:          "Minecraft data migration operation queued.",
		StartedAt:       now,
		UpdatedAt:       now,
		Rollback:        operationRollback{State: "not_started"},
	}
	if err := s.persist(journal); err != nil {
		_ = s.releaseDataMigrationLock()
		return operationJournal{}, false, err
	}
	s.operations[id] = journal
	s.currentDataMigrationID = id
	return journal, true, nil
}

func (s *operationStore) beginMigrationExport(planFingerprint string) (operationJournal, bool, error) {
	if !operationFingerprintPattern.MatchString(planFingerprint) {
		return operationJournal{}, false, errors.New("invalid server migration export plan fingerprint")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.currentMigrationID != "" {
		current := s.operations[s.currentMigrationID]
		if current.PlanFingerprint == planFingerprint && current.OperationType == operationTypeMigrationExport {
			return current, false, nil
		}
		return operationJournal{}, false, errMigrationOperationBusy
	}
	if err := s.acquireMigrationLock(); err != nil {
		return operationJournal{}, false, err
	}

	id, err := newOperationID()
	if err != nil {
		_ = s.releaseMigrationLock()
		return operationJournal{}, false, err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	journal := operationJournal{
		SchemaVersion:   operationSchemaVersion,
		OperationID:     id,
		OperationType:   operationTypeMigrationExport,
		PlanFingerprint: planFingerprint,
		State:           operationQueued,
		Stage:           "queued",
		Status:          "Server migration export operation queued.",
		StartedAt:       now,
		UpdatedAt:       now,
		Rollback:        operationRollback{State: "not_started"},
	}
	if err := s.persist(journal); err != nil {
		_ = s.releaseMigrationLock()
		return operationJournal{}, false, err
	}
	s.operations[id] = journal
	s.currentMigrationID = id
	return journal, true, nil
}

func (s *operationStore) beginMigrationImport(planFingerprint string) (operationJournal, bool, error) {
	if !operationFingerprintPattern.MatchString(planFingerprint) {
		return operationJournal{}, false, errors.New("invalid server migration import plan fingerprint")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.currentMigrationID != "" {
		current := s.operations[s.currentMigrationID]
		if current.PlanFingerprint == planFingerprint && current.OperationType == operationTypeMigrationImport {
			return current, false, nil
		}
		return operationJournal{}, false, errMigrationOperationBusy
	}
	if err := s.acquireMigrationLock(); err != nil {
		return operationJournal{}, false, err
	}

	id, err := newOperationID()
	if err != nil {
		_ = s.releaseMigrationLock()
		return operationJournal{}, false, err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	journal := operationJournal{
		SchemaVersion:   operationSchemaVersion,
		OperationID:     id,
		OperationType:   operationTypeMigrationImport,
		PlanFingerprint: planFingerprint,
		State:           operationQueued,
		Stage:           "queued",
		Status:          "Server migration import operation queued.",
		StartedAt:       now,
		UpdatedAt:       now,
		Rollback:        operationRollback{State: "not_started"},
	}
	if err := s.persist(journal); err != nil {
		_ = s.releaseMigrationLock()
		return operationJournal{}, false, err
	}
	s.operations[id] = journal
	s.currentMigrationID = id
	return journal, true, nil
}

func (s *operationStore) beginMigrationRecovery(planFingerprint string) (operationJournal, bool, error) {
	if !operationFingerprintPattern.MatchString(planFingerprint) {
		return operationJournal{}, false, errors.New("invalid server migration recovery plan fingerprint")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var handedOff *operationJournal
	if s.currentMigrationID != "" {
		current := s.operations[s.currentMigrationID]
		if current.PlanFingerprint == planFingerprint && current.OperationType == operationTypeMigrationRecovery {
			return current, false, nil
		}
		if current.OperationType != operationTypeMigrationImport || current.State != operationNeedsAttention {
			return operationJournal{}, false, errMigrationOperationBusy
		}
		original := current
		handedOff = &original
		now := s.now().UTC().Format(time.RFC3339Nano)
		current.State = operationRolledBack
		current.Stage = "recovery_handoff"
		current.Status = "Import recovery responsibility transferred to a dedicated server migration Recovery operation."
		current.UpdatedAt = now
		current.FinishedAt = now
		current.Rollback.State = "delegated"
		current.Rollback.Result = "recovery_handoff"
		if err := s.persist(current); err != nil {
			return operationJournal{}, false, err
		}
		s.operations[current.OperationID] = current
		s.currentMigrationID = ""
	}
	if err := s.acquireMigrationLock(); err != nil {
		if handedOff != nil {
			_ = s.persist(*handedOff)
			s.operations[handedOff.OperationID] = *handedOff
			s.currentMigrationID = handedOff.OperationID
		}
		return operationJournal{}, false, err
	}
	id, err := newOperationID()
	if err != nil {
		if handedOff != nil {
			_ = s.persist(*handedOff)
			s.operations[handedOff.OperationID] = *handedOff
			s.currentMigrationID = handedOff.OperationID
		} else {
			_ = s.releaseMigrationLock()
		}
		return operationJournal{}, false, err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	journal := operationJournal{SchemaVersion:operationSchemaVersion, OperationID:id, OperationType:operationTypeMigrationRecovery, PlanFingerprint:planFingerprint, State:operationQueued, Stage:"queued", Status:"Server migration recovery operation queued.", StartedAt:now, UpdatedAt:now, Rollback:operationRollback{State:"not_started"}}
	if err := s.persist(journal); err != nil {
		if handedOff != nil {
			_ = s.persist(*handedOff)
			s.operations[handedOff.OperationID] = *handedOff
			s.currentMigrationID = handedOff.OperationID
		} else {
			_ = s.releaseMigrationLock()
		}
		return operationJournal{}, false, err
	}
	s.operations[id] = journal
	s.currentMigrationID = id
	return journal, true, nil
}
func (s *operationStore) get(id string) (operationJournal, error) {
	if !validOperationID(id) {
		return operationJournal{}, errOperationNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	journal, ok := s.operations[id]
	if !ok {
		return operationJournal{}, errOperationNotFound
	}
	return journal, nil
}

func (s *operationStore) currentSetup() (*operationJournal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentSetupID == "" {
		return nil, nil
	}
	journal, ok := s.operations[s.currentSetupID]
	if !ok {
		return nil, errors.New("current setup operation journal is missing")
	}
	copy := journal
	return &copy, nil
}


func (s *operationStore) currentRestore() (*operationJournal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentRestoreID == "" {
		return nil, nil
	}
	journal, ok := s.operations[s.currentRestoreID]
	if !ok {
		return nil, errors.New("current restore operation journal is missing")
	}
	copy := journal
	return &copy, nil
}


func (s *operationStore) currentDataMigration() (*operationJournal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentDataMigrationID == "" {
		return nil, nil
	}
	journal, ok := s.operations[s.currentDataMigrationID]
	if !ok {
		return nil, errors.New("current data migration operation journal is missing")
	}
	copy := journal
	return &copy, nil
}

func (s *operationStore) currentMigration() (*operationJournal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentMigrationID == "" {
		return nil, nil
	}
	journal, ok := s.operations[s.currentMigrationID]
	if !ok {
		return nil, errors.New("current server migration operation journal is missing")
	}
	copy := journal
	return &copy, nil
}

func (s *operationStore) transition(id string, next operationState, stage, status string) (operationJournal, error) {
	if !validOperationID(id) {
		return operationJournal{}, errOperationNotFound
	}
	if !validOperationState(next) {
		return operationJournal{}, errors.New("invalid operation state")
	}
	if stage == "" || !operationStagePattern.MatchString(stage) {
		return operationJournal{}, errors.New("invalid operation stage")
	}
	if !validOperationStatus(status) {
		return operationJournal{}, errors.New("invalid operation status")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	journal, ok := s.operations[id]
	if !ok {
		return operationJournal{}, errOperationNotFound
	}
	if !operationTransitionAllowed(journal.State, next) {
		return operationJournal{}, fmt.Errorf("invalid operation transition %s -> %s", journal.State, next)
	}
	journal.State = next
	journal.Stage = stage
	journal.Status = status
	journal.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
	if next == operationRollingBack {
		journal.Rollback.State = "running"
	}
	if next == operationRolledBack {
		journal.Rollback.State = "succeeded"
		journal.Rollback.Result = "rolled_back"
	}
	if next == operationSucceeded || next == operationRolledBack {
		journal.FinishedAt = journal.UpdatedAt
	}
	if err := s.persist(journal); err != nil {
		return operationJournal{}, err
	}
	s.operations[id] = journal
	if next == operationSucceeded || next == operationRolledBack {
		switch journal.OperationType {
		case operationTypeSetup:
			s.currentSetupID = ""
			if err := s.releaseSetupLock(); err != nil {
				return operationJournal{}, err
			}
		case operationTypeRestore:
			s.currentRestoreID = ""
			if err := s.releaseRestoreLock(); err != nil {
				return operationJournal{}, err
			}
		case operationTypeDataMigration:
			s.currentDataMigrationID = ""
			if err := s.releaseDataMigrationLock(); err != nil {
				return operationJournal{}, err
			}
		case operationTypeMigrationExport, operationTypeMigrationImport, operationTypeMigrationRecovery:
			s.currentMigrationID = ""
			if err := s.releaseMigrationLock(); err != nil {
				return operationJournal{}, err
			}
		}
	}
	return journal, nil
}

func operationTransitionAllowed(current, next operationState) bool {
	switch current {
	case operationQueued:
		return next == operationValidating || next == operationFailed || next == operationNeedsAttention
	case operationValidating:
		return next == operationRunning || next == operationFailed || next == operationNeedsAttention
	case operationRunning:
		return next == operationVerifying || next == operationFailed || next == operationNeedsAttention
	case operationVerifying:
		return next == operationSucceeded || next == operationFailed || next == operationNeedsAttention
	case operationFailed:
		return next == operationRollingBack || next == operationNeedsAttention
	case operationRollingBack:
		return next == operationRolledBack || next == operationNeedsAttention
	default:
		return false
	}
}

func (s *operationStore) acquireSetupLock() error {
	if s.setupLockHeld {
		return nil
	}
	if err := syscall.Flock(int(s.lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return errSetupLockBusy
		}
		return fmt.Errorf("acquire setup operation lock: %w", err)
	}
	s.setupLockHeld = true
	return nil
}

func (s *operationStore) releaseSetupLock() error {
	if !s.setupLockHeld {
		return nil
	}
	if err := syscall.Flock(int(s.lockFile.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("release setup operation lock: %w", err)
	}
	s.setupLockHeld = false
	return nil
}


func (s *operationStore) acquireRestoreLock() error {
	if s.restoreLockHeld {
		return nil
	}
	if s.restoreLockFile == nil {
		return errors.New("restore operation lock is unavailable")
	}
	if err := syscall.Flock(int(s.restoreLockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return errRestoreLockBusy
		}
		return fmt.Errorf("acquire restore operation lock: %w", err)
	}
	s.restoreLockHeld = true
	return nil
}

func (s *operationStore) releaseRestoreLock() error {
	if !s.restoreLockHeld {
		return nil
	}
	if s.restoreLockFile == nil {
		return errors.New("restore operation lock is unavailable")
	}
	if err := syscall.Flock(int(s.restoreLockFile.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("release restore operation lock: %w", err)
	}
	s.restoreLockHeld = false
	return nil
}


func (s *operationStore) acquireDataMigrationLock() error {
	if s.dataMigrationLockHeld {
		return nil
	}
	if s.dataMigrationLockFile == nil {
		return errors.New("data migration operation lock is unavailable")
	}
	if err := syscall.Flock(int(s.dataMigrationLockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return errDataMigrationLockBusy
		}
		return fmt.Errorf("acquire data migration operation lock: %w", err)
	}
	s.dataMigrationLockHeld = true
	return nil
}

func (s *operationStore) releaseDataMigrationLock() error {
	if !s.dataMigrationLockHeld {
		return nil
	}
	if s.dataMigrationLockFile == nil {
		return errors.New("data migration operation lock is unavailable")
	}
	if err := syscall.Flock(int(s.dataMigrationLockFile.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("release data migration operation lock: %w", err)
	}
	s.dataMigrationLockHeld = false
	return nil
}

func (s *operationStore) acquireMigrationLock() error {
	if s.migrationLockHeld {
		return nil
	}
	if s.migrationLockFile == nil {
		return errors.New("server migration operation lock is unavailable")
	}
	if err := syscall.Flock(int(s.migrationLockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return errMigrationLockBusy
		}
		return fmt.Errorf("acquire server migration operation lock: %w", err)
	}
	s.migrationLockHeld = true
	return nil
}

func (s *operationStore) releaseMigrationLock() error {
	if !s.migrationLockHeld {
		return nil
	}
	if s.migrationLockFile == nil {
		return errors.New("server migration operation lock is unavailable")
	}
	if err := syscall.Flock(int(s.migrationLockFile.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("release server migration operation lock: %w", err)
	}
	s.migrationLockHeld = false
	return nil
}

func (s *operationStore) persist(journal operationJournal) error {
	if err := validateOperationJournal(journal); err != nil {
		return err
	}
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(s.operationsDir, journal.OperationID+".json")
	tmp, err := os.CreateTemp(s.operationsDir, ".operation-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
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
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	dir, err := os.Open(s.operationsDir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func newOperationID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}
