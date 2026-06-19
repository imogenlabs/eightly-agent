package tlsutil

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsureCertGeneratesFiles(t *testing.T) {
	dir := t.TempDir()

	certPath, keyPath, err := EnsureCert(dir)
	if err != nil {
		t.Fatalf("EnsureCert failed: %v", err)
	}

	if certPath != filepath.Join(dir, "cert.pem") {
		t.Fatalf("unexpected cert path: %s", certPath)
	}
	if keyPath != filepath.Join(dir, "key.pem") {
		t.Fatalf("unexpected key path: %s", keyPath)
	}

	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("cert file does not exist: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("key file does not exist: %v", err)
	}
}

func TestGeneratedCertIsValidX509(t *testing.T) {
	dir := t.TempDir()

	certPath, keyPath, err := EnsureCert(dir)
	if err != nil {
		t.Fatalf("EnsureCert failed: %v", err)
	}

	certData, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("failed to read cert: %v", err)
	}

	block, _ := pem.Decode(certData)
	if block == nil {
		t.Fatal("failed to decode PEM block from cert")
	}
	if block.Type != "CERTIFICATE" {
		t.Fatalf("expected PEM type CERTIFICATE, got %s", block.Type)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse X509 certificate: %v", err)
	}

	if cert.Subject.CommonName != "eightly-agent" {
		t.Fatalf("expected CN eightly-agent, got %q", cert.Subject.CommonName)
	}

	// Verify key file is valid PEM RSA key
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("failed to read key: %v", err)
	}
	keyBlock, _ := pem.Decode(keyData)
	if keyBlock == nil {
		t.Fatal("failed to decode PEM block from key")
	}
	if keyBlock.Type != "RSA PRIVATE KEY" {
		t.Fatalf("expected PEM type RSA PRIVATE KEY, got %s", keyBlock.Type)
	}
	_, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		t.Fatalf("failed to parse PKCS1 private key: %v", err)
	}
}

func TestCertSANs(t *testing.T) {
	dir := t.TempDir()

	certPath, _, err := EnsureCert(dir)
	if err != nil {
		t.Fatalf("EnsureCert failed: %v", err)
	}

	certData, _ := os.ReadFile(certPath)
	block, _ := pem.Decode(certData)
	cert, _ := x509.ParseCertificate(block.Bytes)

	foundLocalhost := false
	for _, name := range cert.DNSNames {
		if name == "localhost" {
			foundLocalhost = true
			break
		}
	}
	if !foundLocalhost {
		t.Fatalf("expected localhost in DNS SANs, got %v", cert.DNSNames)
	}

	foundLoopback := false
	for _, ip := range cert.IPAddresses {
		if ip.Equal(net.ParseIP("127.0.0.1")) {
			foundLoopback = true
			break
		}
	}
	if !foundLoopback {
		t.Fatalf("expected 127.0.0.1 in IP SANs, got %v", cert.IPAddresses)
	}
}

func TestCertExtraSANs(t *testing.T) {
	dir := t.TempDir()

	certPath, _, err := EnsureCert(dir, "myhost.local", "192.168.1.50", "agent.eight.ly")
	if err != nil {
		t.Fatalf("EnsureCert with extra SANs failed: %v", err)
	}

	certData, _ := os.ReadFile(certPath)
	block, _ := pem.Decode(certData)
	cert, _ := x509.ParseCertificate(block.Bytes)

	wantDNS := map[string]bool{"localhost": false, "myhost.local": false, "agent.eight.ly": false}
	for _, name := range cert.DNSNames {
		if _, ok := wantDNS[name]; ok {
			wantDNS[name] = true
		}
	}
	for name, found := range wantDNS {
		if !found {
			t.Errorf("expected %q in DNS SANs, got %v", name, cert.DNSNames)
		}
	}

	wantIPs := []string{"127.0.0.1", "192.168.1.50"}
	for _, want := range wantIPs {
		found := false
		for _, ip := range cert.IPAddresses {
			if ip.Equal(net.ParseIP(want)) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %s in IP SANs, got %v", want, cert.IPAddresses)
		}
	}
}

func TestCertProperties(t *testing.T) {
	dir := t.TempDir()

	certPath, _, err := EnsureCert(dir)
	if err != nil {
		t.Fatalf("EnsureCert failed: %v", err)
	}

	certData, _ := os.ReadFile(certPath)
	block, _ := pem.Decode(certData)
	cert, _ := x509.ParseCertificate(block.Bytes)

	if cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		t.Fatal("expected KeyUsageDigitalSignature")
	}
	if cert.KeyUsage&x509.KeyUsageKeyEncipherment == 0 {
		t.Fatal("expected KeyUsageKeyEncipherment")
	}

	hasServerAuth := false
	for _, usage := range cert.ExtKeyUsage {
		if usage == x509.ExtKeyUsageServerAuth {
			hasServerAuth = true
		}
	}
	if !hasServerAuth {
		t.Fatal("expected ExtKeyUsageServerAuth")
	}

	if cert.IsCA {
		t.Fatal("cert should not be a CA")
	}
}

func TestEnsureCertIdempotent(t *testing.T) {
	dir := t.TempDir()

	certPath1, keyPath1, err := EnsureCert(dir)
	if err != nil {
		t.Fatalf("first EnsureCert failed: %v", err)
	}

	certData1, _ := os.ReadFile(certPath1)
	keyData1, _ := os.ReadFile(keyPath1)

	// Call again — should return same paths, not regenerate
	certPath2, keyPath2, err := EnsureCert(dir)
	if err != nil {
		t.Fatalf("second EnsureCert failed: %v", err)
	}

	if certPath1 != certPath2 {
		t.Fatalf("cert paths differ: %s vs %s", certPath1, certPath2)
	}
	if keyPath1 != keyPath2 {
		t.Fatalf("key paths differ: %s vs %s", keyPath1, keyPath2)
	}

	certData2, _ := os.ReadFile(certPath2)
	keyData2, _ := os.ReadFile(keyPath2)

	if string(certData1) != string(certData2) {
		t.Fatal("cert was regenerated on second call — should be idempotent")
	}
	if string(keyData1) != string(keyData2) {
		t.Fatal("key was regenerated on second call — should be idempotent")
	}
}

func TestEnsureCertCreatesSubdirectory(t *testing.T) {
	base := t.TempDir()
	nested := filepath.Join(base, "sub", "dir")

	certPath, keyPath, err := EnsureCert(nested)
	if err != nil {
		t.Fatalf("EnsureCert with nested dir failed: %v", err)
	}

	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("cert not created in nested dir: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("key not created in nested dir: %v", err)
	}
}

func TestKeyFilePermissions(t *testing.T) {
	dir := t.TempDir()

	_, keyPath, err := EnsureCert(dir)
	if err != nil {
		t.Fatalf("EnsureCert failed: %v", err)
	}

	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("failed to stat key: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Fatalf("expected key permissions 0600, got %04o", perm)
	}
}

// TestCertBasicConstraintsValid covers fix #7: the generated leaf must carry an
// explicit, valid BasicConstraints extension marking it as a non-CA.
func TestCertBasicConstraintsValid(t *testing.T) {
	dir := t.TempDir()
	certPath, _, err := EnsureCert(dir)
	if err != nil {
		t.Fatalf("EnsureCert failed: %v", err)
	}
	certData, _ := os.ReadFile(certPath)
	block, _ := pem.Decode(certData)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	if !cert.BasicConstraintsValid {
		t.Error("expected BasicConstraintsValid=true on generated cert")
	}
	if cert.IsCA {
		t.Error("generated leaf must not be a CA")
	}
}

// TestEnsureCertReuseTightensLoosePerms covers fix #7: reusing an existing key
// that is group/world readable must repair the mode back to 0600.
func TestEnsureCertReuseTightensLoosePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX perms not meaningful on Windows")
	}
	dir := t.TempDir()
	_, keyPath, err := EnsureCert(dir)
	if err != nil {
		t.Fatalf("first EnsureCert: %v", err)
	}
	// Loosen the key, simulating a botched deploy or bad umask.
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatalf("chmod loose: %v", err)
	}
	// Reuse path must tighten it back.
	if _, _, err := EnsureCert(dir); err != nil {
		t.Fatalf("reuse EnsureCert: %v", err)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("reuse path should tighten key to 0600, got %04o", perm)
	}
}
