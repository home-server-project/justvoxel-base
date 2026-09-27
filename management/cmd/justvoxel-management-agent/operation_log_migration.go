package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// The old per-type directories are moved once, after every source and destination
// has been checked. An interrupted or conflicting move leaves its files in place.
func validateOperationDiagnosticMigration(source, logsDir string) error {
	info, err := os.Lstat(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe old operation log directory")
	}
	if err := validatePrivateDiagnosticDirectory(source); err != nil {
		return err
	}
	types, err := os.ReadDir(source)
	if err != nil { return err }
	if len(types) > 8 { return fmt.Errorf("too many old operation log directories") }
	count := 0
	seen := make(map[string]bool)
	for _, kind := range types {
		category := diagnosticCategoryForOperationType(kind.Name())
		if category == "" || kind.Type()&os.ModeSymlink != 0 || !kind.IsDir() {
			return fmt.Errorf("unsafe old operation log category %q", kind.Name())
		}
		oldDir := filepath.Join(source, kind.Name())
		if err := validatePrivateDiagnosticDirectory(oldDir); err != nil { return err }
		files, err := os.ReadDir(oldDir)
		if err != nil { return err }
		count += len(files)
		if count > 1000 { return fmt.Errorf("too many old operation logs") }
		destination := filepath.Join(logsDir, category+"-logs")
		if destInfo, err := os.Lstat(destination); err == nil {
			if !destInfo.IsDir() || destInfo.Mode()&os.ModeSymlink != 0 { return fmt.Errorf("unsafe destination %s", destination) }
			if err := validatePrivateDiagnosticDirectory(destination); err != nil { return err }
			destFiles, err := os.ReadDir(destination)
			if err != nil { return err }
			if len(files) > 0 && len(destFiles) > 0 { return fmt.Errorf("both %s and %s contain logs", oldDir, destination) }
		} else if !os.IsNotExist(err) { return err }
		for _, file := range files {
			id := strings.TrimSuffix(file.Name(), ".log")
			if !strings.HasSuffix(file.Name(), ".log") || !validOperationID(id) || file.Type() != 0 {
				return fmt.Errorf("unsafe old operation log %q", file.Name())
			}
			fileInfo, err := file.Info()
			if err != nil { return err }
			if !fileInfo.Mode().IsRegular() || fileInfo.Mode().Perm() != 0o600 || fileInfo.Size() > operationDiagnosticLimit {
				return fmt.Errorf("unsafe old operation log %q", file.Name())
			}
			owner, ok := fileInfo.Sys().(*syscall.Stat_t)
			if !ok || owner.Uid != uint32(os.Geteuid()) { return fmt.Errorf("unsafe old operation log owner") }
			target := filepath.Join(destination, file.Name())
			if seen[target] { return fmt.Errorf("duplicate old operation log %q", file.Name()) }
			seen[target] = true
			if _, err := os.Lstat(target); err == nil { return fmt.Errorf("operation log destination already exists") } else if !os.IsNotExist(err) { return err }
		}
	}
	return nil
}

func migrateOperationDiagnostics(source, logsDir string) error {
	types, err := os.ReadDir(source)
	if os.IsNotExist(err) { return nil }
	if err != nil { return err }
	for _, kind := range types {
		oldDir := filepath.Join(source, kind.Name())
		destination := filepath.Join(logsDir, diagnosticCategoryForOperationType(kind.Name())+"-logs")
		if err := ensurePrivateDirectory(destination); err != nil { return err }
		files, err := os.ReadDir(oldDir)
		if err != nil { return err }
		for _, file := range files {
			// Link refuses an existing name, including one created after preflight.
			// Source and destination share the same management filesystem.
			dest := filepath.Join(destination, file.Name())
			src := filepath.Join(oldDir, file.Name())
			if err := os.Link(src, dest); err != nil { return err }
			info, err := os.Lstat(dest)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() > operationDiagnosticLimit {
				_ = os.Remove(dest)
				return fmt.Errorf("unsafe migrated operation log %q", dest)
			}
			if err := os.Remove(src); err != nil { return err }
		}
		if err := os.Remove(oldDir); err != nil { return err }
	}
	return os.Remove(source)
}
