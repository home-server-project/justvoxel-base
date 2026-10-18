package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Only explicit configuration roots are walked. openat2 rejects symlinks in
// every component. Worlds, backup archives, WebUI state and Podman never enter
// this inventory. The same bytes form review evidence and the optional archive.
func (b *instanceRuntimeBackend) startOverConfiguration(entries []instanceDeleteJournal) (map[string][]byte, error) {
	roots := []string{startOverPolicyRoot}
	for _, j := range entries {
		dir, _, q := b.paths(j.ID)
		roots = append(roots, dir, q, filepath.Join(instanceBackupUnits, instanceBackupUnit(j.ID, "service")), filepath.Join(instanceBackupUnits, instanceBackupUnit(j.ID, "timer")), b.deletePath(j.ID))
	}
	files := map[string][]byte{}
	total := 0
	var visit func(string) error
	visit = func(path string) error {
		f, err := instanceOpen(path, syscall.O_RDONLY|syscall.O_NONBLOCK)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return err
		}
		owner, ok := st.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(b.identities().uid) || st.Mode().Perm()&0022 != 0 {
			return errInstanceRecovery
		}
		if st.IsDir() {
			names, err := f.Readdirnames(-1)
			if err != nil {
				return err
			}
			sort.Strings(names)
			for _, name := range names {
				if err = visit(filepath.Join(path, name)); err != nil {
					return err
				}
			}
			return nil
		}
		if !st.Mode().IsRegular() || st.Size() > 8<<20 || len(files) >= 16384 {
			return errInstanceRecovery
		}
		raw, err := io.ReadAll(io.LimitReader(f, 8<<20+1))
		if err != nil || len(raw) > 8<<20 {
			return errInstanceRecovery
		}
		total += len(raw)
		if total > 64<<20 {
			return errInstanceRecovery
		}
		files[path] = raw
		return nil
	}
	for _, root := range roots {
		if err := visit(root); err != nil {
			return nil, err
		}
	}
	return files, nil
}
func (b *instanceRuntimeBackend) startOverConfigurationEvidence(entries []instanceDeleteJournal) (map[string]string, error) {
	files, err := b.startOverConfiguration(entries)
	if err != nil {
		return nil, err
	}
	for _, j := range entries {
		delete(files, b.deletePath(j.ID))
	}
	out := map[string]string{}
	for path, raw := range files {
		out[path] = instanceHash(raw)
	}
	return out, nil
}
func (b *instanceRuntimeBackend) archiveStartOverConfiguration(entries []instanceDeleteJournal) (string, error) {
	files, err := b.startOverConfiguration(entries)
	if err != nil {
		return "", err
	}
	dir, err := b.privateDirectory(startOverArchiveRoot, true)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	partial, err := os.CreateTemp(startOverArchiveRoot, "justvoxel-config-"+time.Now().UTC().Format("20060102-150405")+"-*.partial")
	if err != nil {
		return "", err
	}
	path := partial.Name()
	defer os.Remove(path)
	defer partial.Close()
	if err = partial.Chmod(0600); err != nil {
		return "", err
	}
	if err = partial.Chown(0, 0); err != nil {
		return "", err
	}
	gz := gzip.NewWriter(partial)
	tw := tar.NewWriter(gz)
	paths := []string{}
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		raw := files[path]
		if err = tw.WriteHeader(&tar.Header{Name: strings.TrimPrefix(path, "/"), Mode: 0600, Uid: 0, Gid: 0, Size: int64(len(raw)), Typeflag: tar.TypeReg}); err != nil {
			return "", err
		}
		if _, err = tw.Write(raw); err != nil {
			return "", err
		}
	}
	if err = tw.Close(); err != nil {
		return "", err
	}
	if err = gz.Close(); err != nil {
		return "", err
	}
	if err = partial.Sync(); err != nil {
		return "", err
	}
	if _, err = partial.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	reader, err := gzip.NewReader(partial)
	if err != nil {
		return "", err
	}
	tr := tar.NewReader(reader)
	for _, path := range paths {
		header, e := tr.Next()
		if e != nil {
			reader.Close()
			return "", e
		}
		if header.Name != strings.TrimPrefix(path, "/") || header.Typeflag != tar.TypeReg || header.Mode != 0600 || header.Uid != 0 || header.Gid != 0 || header.Size != int64(len(files[path])) {
			reader.Close()
			return "", errInstanceRecovery
		}
		raw, e := io.ReadAll(io.LimitReader(tr, 8<<20+1))
		if e != nil || instanceHash(raw) != instanceHash(files[path]) {
			reader.Close()
			return "", errInstanceRecovery
		}
	}
	if _, err = tr.Next(); err != io.EOF {
		reader.Close()
		return "", errInstanceRecovery
	}
	// Consume the gzip trailer too, so corrupt compressed output cannot publish.
	if _, err = io.Copy(io.Discard, reader); err != nil {
		reader.Close()
		return "", err
	}
	if err = reader.Close(); err != nil {
		return "", err
	}
	if err = partial.Close(); err != nil {
		return "", err
	}
	final := strings.TrimSuffix(path, ".partial") + ".tar.gz"
	if err = os.Rename(path, final); err != nil {
		return "", err
	}
	if err = dir.Sync(); err != nil {
		return "", err
	}
	return final, nil
}

var startOverFstabPath = "/etc/fstab"

// Pin preserved policy contents plus mount/data/backup directory identities.
// This is verification only: no mount, credential, backup or fstab writer exists
// in Start Over. The active policy pair is deliberately excluded.
func (b *instanceRuntimeBackend) startOverPreservationEvidence(entries []instanceDeleteJournal, legacyBackupPath string) (map[string]string, error) {
	files, err := b.startOverConfiguration(nil)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for path, raw := range files {
		if path == filepath.Join(startOverPolicyRoot, "justvoxel.conf") || path == filepath.Join(startOverPolicyRoot, "minecraft-backup.env") || instanceRuntimeInside(path, b.root) {
			continue
		}
		out[path] = instanceHash(raw)
	}
	f, err := instanceOpen(startOverFstabPath, syscall.O_RDONLY)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	f.Close()
	if err != nil || len(raw) > 1<<20 {
		return nil, errInstanceRecovery
	}
	out[startOverFstabPath] = instanceHash(raw)
	// The value marks paths that must exist. Backup namespaces and the legacy
	// backup destination may be absent; data roots and modern backup roots may not.
	roots := map[string]bool{}
	addRoot := func(path string, required bool) {
		roots[path] = roots[path] || required
	}
	for _, j := range entries {
		addRoot(j.Record.Storage.Root, true)
		if j.Record.Backups != nil {
			addRoot(j.Record.Backups.Root, true)
			addRoot(j.Record.Backups.Path, false)
		} else if legacyBackupPath != "" {
			addRoot(legacyBackupPath, false)
		}
	}
	// Use the reviewed paths even after the active setup policy is removed.
	for root := range roots {
		f, err := instanceOpen(root, syscall.O_RDONLY|syscall.O_DIRECTORY)
		if errors.Is(err, os.ErrNotExist) && !roots[root] {
			out[root] = "absent"
			continue
		}
		if err != nil {
			return nil, err
		}
		st, err := f.Stat()
		f.Close()
		if err != nil {
			return nil, err
		}
		v, ok := st.Sys().(*syscall.Stat_t)
		if !ok {
			return nil, errInstanceRecovery
		}
		out[root] = fmt.Sprintf("%d:%d", v.Dev, v.Ino)
	}
	return out, nil
}
