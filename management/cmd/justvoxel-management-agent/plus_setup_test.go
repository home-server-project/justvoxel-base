package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validPlusSetupRequest() plusSetupRequest {
	return plusSetupRequest{Components: plusSetupComponents{Database: true, Cache: true, Panel: true, Wings: true, Drydock: true}, Host: "panel.example.com", Username: "ignored", Email: "admin@example.com", Password: "A-valid-password-123"}
}

func TestPlusSetupValidationAndIndependentIdentity(t *testing.T) {
	now := time.Now()
	request := validPlusSetupRequest()
	if err := validatePlusSetup(&request, "hostadmin", nil, now); err != nil {
		t.Fatal(err)
	}
	if request.Username != "hostadmin" {
		t.Fatal("host username was not reused")
	}
	request.SeparateAccount = true
	request.Username = "paneladmin"
	if err := validatePlusSetup(&request, "hostadmin", nil, now); err != nil || request.Username != "paneladmin" {
		t.Fatal("separate identity was not preserved")
	}
	for _, change := range []func(*plusSetupRequest){
		func(r *plusSetupRequest) { r.Components = plusSetupComponents{} },
		func(r *plusSetupRequest) { r.Host = "https://example.com/path" },
		func(r *plusSetupRequest) { r.Host = "example.com\nEVIL=value" },
		func(r *plusSetupRequest) { r.UseDomain = true; r.Host = "192.168.1.2" },
		func(r *plusSetupRequest) { r.Password = "short" },
		func(r *plusSetupRequest) { r.Email = "Admin <admin@example.com>" },
		func(r *plusSetupRequest) { r.StorageMount = "/unmounted" },
		func(r *plusSetupRequest) { r.UseTLS = true; r.Certificate = "not a certificate" },
	} {
		request := validPlusSetupRequest()
		change(&request)
		if err := validatePlusSetup(&request, "hostadmin", nil, now); err == nil {
			t.Fatal("unsafe configuration accepted")
		}
	}
	// Partial selections are administrator choices; no artificial dependency lock.
	request = validPlusSetupRequest()
	request.Components = plusSetupComponents{Wings: true}
	if err := validatePlusSetup(&request, "hostadmin", nil, now); err != nil {
		t.Fatal(err)
	}
	if request.Password != "" || request.Email != "" || request.Username != "" {
		t.Fatal("unneeded account secrets retained")
	}
}

func plusSetupCertificate(t *testing.T, now time.Time, host string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
}

func TestPlusSetupCertificateChecks(t *testing.T) {
	now := time.Now()
	request := validPlusSetupRequest()
	request.UseTLS = true
	request.Certificate, request.PrivateKey = plusSetupCertificate(t, now, request.Host)
	if err := validatePlusSetup(&request, "admin", nil, now); err != nil {
		t.Fatal(err)
	}
	if err := validatePlusSetup(&request, "admin", nil, now.Add(2*time.Hour)); err == nil {
		t.Fatal("expired certificate accepted")
	}
	request.Host = "other.example.com"
	if err := validatePlusSetup(&request, "admin", nil, now); err == nil {
		t.Fatal("wrong hostname accepted")
	}
	request.Host = "panel.example.com"
	_, request.PrivateKey = plusSetupCertificate(t, now, request.Host)
	if err := validatePlusSetup(&request, "admin", nil, now); err == nil {
		t.Fatal("mismatched key accepted")
	}
	request.UseTLS = false
	if err := validatePlusSetup(&request, "admin", nil, now); err != nil {
		t.Fatal(err)
	}
	if request.Certificate != "" || request.PrivateKey != "" {
		t.Fatal("disabled certificate secrets retained")
	}
}

func TestPlusSetupStorageEligibility(t *testing.T) {
	mounts := eligiblePlusSetupMounts([]plusSetupMount{
		{Target: "/mnt/data", FSType: "xfs", Options: "rw,relatime", Source: "/dev/vdb1"},
		{Target: "/mnt/readonly", FSType: "ext4", Options: "ro", Source: "/dev/vdc1"},
		{Target: "/mnt/network", FSType: "nfs", Options: "rw", Source: "server:/share"},
		{Target: "/var", FSType: "xfs", Options: "rw", Source: "/dev/vda1"},
		{Target: "/mnt/tmp", FSType: "tmpfs", Options: "rw", Source: "tmpfs"},
	})
	if len(mounts) != 1 || mounts[0].Target != "/mnt/data" || mounts[0].Source != "" {
		t.Fatalf("unexpected mounts: %+v", mounts)
	}
}

func TestPlusSetupPreparationPermissionsAndSymlinkRejection(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(parent, "prepare")
	request := validPlusSetupRequest()
	if err := savePlusSetup(directory, request); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{directory: 0700, filepath.Join(directory, "setup.json"): 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("unsafe mode for %s", path)
		}
	}
	data, err := os.ReadFile(filepath.Join(directory, "setup.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved plusSetupRequest
	if err := json.Unmarshal(data, &saved); err != nil || saved.Password != request.Password {
		t.Fatal("preparation was not saved")
	}
	victim := filepath.Join(parent, "victim")
	if err := os.WriteFile(victim, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(directory, "setup.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(directory, "setup.json")); err != nil {
		t.Fatal(err)
	}
	if err := savePlusSetup(directory, request); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(victim)
	if string(data) != "unchanged" {
		t.Fatal("followed target symlink")
	}
	linked := filepath.Join(parent, "linked")
	if err := os.Symlink(directory, linked); err != nil {
		t.Fatal(err)
	}
	if err := savePlusSetup(linked, request); err == nil {
		t.Fatal("symlink directory accepted")
	}
}

func TestPlusSetupStrictDecodingAndRoles(t *testing.T) {
	for _, body := range []string{`{"unknown":true}`, `{} {}`, strings.Repeat("x", plusSetupBodyLimit+1)} {
		rr := httptest.NewRecorder()
		var request plusSetupRequest
		if decodePlusSetup(rr, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), &request) || rr.Code != http.StatusBadRequest {
			t.Fatal("invalid body accepted")
		}
	}
	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		s.plus = true
		for _, handler := range []http.HandlerFunc{s.plusSetupState, s.plusSetupPrepare} {
			rr := httptest.NewRecorder()
			handler(rr, surfaceRequest(http.MethodPost, "/v1/admin/plus/setup/prepare", `{}`))
			if rr.Code != http.StatusForbidden {
				t.Fatalf("%s permitted setup", role)
			}
		}
	}
}
