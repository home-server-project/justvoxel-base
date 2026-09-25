package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"golang.org/x/crypto/argon2"
)

const (
	systemAdminUsername = "voxel"
	authModePath        = stateDir + "/auth-mode.json"
	localAuthPath       = stateDir + "/local-auth.json"
	localAuthVersion    = 1
	authModeVersion     = 1
)

type authMode string

const (
	authModeSystem   authMode = "system"
	authModeSeparate authMode = "separate"
)

type authModeState struct {
	Version int      `json:"version"`
	Mode    authMode `json:"mode"`
}

type localAuthStore struct {
	Version  int            `json:"version"`
	Accounts []localAccount `json:"accounts"`
}

type localAccount struct {
	Username    string `json:"username"`
	Role        string `json:"role"`
	Salt        string `json:"salt"`
	Hash        string `json:"hash"`
	MemoryKiB   uint32 `json:"memory_kib"`
	Iterations  uint32 `json:"iterations"`
	Parallelism uint8  `json:"parallelism"`
	KeyLength   uint32 `json:"key_length"`
}

// currentAuthMode deliberately treats missing mode state as System account.
// This is the upgrade-safe default: an existing bootc/rebased installation
// keeps its real Linux voxel credential and does not inherit the legacy beta
// WebUI password as an authentication source.
func currentAuthMode() (authMode, error) {
	data, err := os.ReadFile(authModePath)
	if errors.Is(err, os.ErrNotExist) {
		return authModeSystem, nil
	}
	if err != nil {
		return "", err
	}
	var state authModeState
	if err := json.Unmarshal(data, &state); err != nil {
		return "", fmt.Errorf("decode authentication mode: %w", err)
	}
	if state.Version != authModeVersion {
		return "", fmt.Errorf("unsupported authentication mode state version %d", state.Version)
	}
	if !validAuthMode(state.Mode) {
		return "", fmt.Errorf("unsupported authentication mode %q", state.Mode)
	}
	return state.Mode, nil
}

func setAuthMode(mode authMode) error {
	if !validAuthMode(mode) {
		return fmt.Errorf("unsupported authentication mode %q", mode)
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(authModeState{Version: authModeVersion, Mode: mode}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWrite(authModePath, data, 0o600)
}

func validAuthMode(mode authMode) bool {
	return mode == authModeSystem || mode == authModeSeparate
}

func resetAuthenticationStateForFactoryReset() error {
	if err := setAuthMode(authModeSystem); err != nil {
		return fmt.Errorf("restore System authentication mode: %w", err)
	}
	if err := os.Remove(localAuthPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove separate WebUI administrator credential: %w", err)
	}
	if err := os.Remove(authModePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove explicit authentication mode state: %w", err)
	}
	return nil
}

// writeLocalAdministrator creates or replaces only the WebUI-local voxel
// administrator record. The versioned account-list format intentionally leaves
// room for future WebUI-only Operator/Viewer identities without creating Linux
// users or changing the system administrator credential.
func writeLocalAdministrator(password string) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	account, err := localAccountForPassword(systemAdminUsername, "administrator", password)
	if err != nil {
		return err
	}

	store := localAuthStore{Version: localAuthVersion}
	if existing, err := readLocalAuthStore(); err == nil {
		store = existing
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	updated := false
	for i := range store.Accounts {
		if store.Accounts[i].Username == systemAdminUsername {
			store.Accounts[i] = account
			updated = true
			break
		}
	}
	if !updated {
		store.Accounts = append(store.Accounts, account)
	}
	return writeLocalAuthStore(store)
}

func verifyLocalAdministrator(username, password string) bool {
	if username != systemAdminUsername {
		return false
	}
	store, err := readLocalAuthStore()
	if err != nil {
		return false
	}
	for _, account := range store.Accounts {
		if account.Username == systemAdminUsername && account.Role == "administrator" {
			return verifyLocalAccount(account, password)
		}
	}
	return false
}

func readLocalAuthStore() (localAuthStore, error) {
	data, err := os.ReadFile(localAuthPath)
	if err != nil {
		return localAuthStore{}, err
	}
	var store localAuthStore
	if err := json.Unmarshal(data, &store); err != nil {
		return localAuthStore{}, fmt.Errorf("decode local authentication store: %w", err)
	}
	if store.Version != localAuthVersion {
		return localAuthStore{}, fmt.Errorf("unsupported local authentication store version %d", store.Version)
	}
	return store, nil
}

func writeLocalAuthStore(store localAuthStore) error {
	if store.Version != localAuthVersion {
		return fmt.Errorf("unsupported local authentication store version %d", store.Version)
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWrite(localAuthPath, data, 0o600)
}

func localAccountForPassword(username, role, password string) (localAccount, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return localAccount{}, err
	}
	hash := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	return localAccount{
		Username:    username,
		Role:        role,
		Salt:        base64.RawStdEncoding.EncodeToString(salt),
		Hash:        base64.RawStdEncoding.EncodeToString(hash),
		MemoryKiB:   argonMemory,
		Iterations:  argonIterations,
		Parallelism: argonParallelism,
		KeyLength:   argonKeyLength,
	}, nil
}

func verifyLocalAccount(account localAccount, password string) bool {
	if account.Username == "" || account.Role == "" || account.MemoryKiB == 0 || account.Iterations == 0 || account.Parallelism == 0 || account.KeyLength == 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(account.Salt)
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(account.Hash)
	if err != nil || len(expected) != int(account.KeyLength) {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, account.Iterations, account.MemoryKiB, account.Parallelism, account.KeyLength)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
