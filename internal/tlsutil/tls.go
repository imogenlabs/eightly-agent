// SPDX-License-Identifier: Apache-2.0

package tlsutil

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/imogenlabs/eightly-agent/internal/winacl"
)

// CertPath is where the agent keeps its TLS certificate inside dir. The
// healthcheck reads it from here to trust exactly the certificate the listener
// serves.
func CertPath(dir string) string { return filepath.Join(dir, "cert.pem") }

// EnsureCert returns paths to a TLS cert and key in dir, generating a self-signed pair if they don't exist.
func EnsureCert(dir string, extraSANs ...string) (certPath, keyPath string, err error) {
	certPath = CertPath(dir)
	keyPath = filepath.Join(dir, "key.pem")

	if fileExists(certPath) && fileExists(keyPath) {
		// Reuse path: the private key must not be readable by anyone else.
		// Tighten it back (0600 on unix, a protected DACL on Windows) and
		// refuse it if that does not hold, rather than serve with a leaked key.
		// The certificate is reused whatever its dates: Eight.ly OS pins this
		// exact certificate, so a new one would cut the agent off.
		if _, err := winacl.ProtectDir(dir); err != nil {
			return "", "", fmt.Errorf("cannot restrict access to %s: %w", dir, err)
		}
		if err := tightenKeyPerm(keyPath); err != nil {
			return "", "", err
		}
		if err := winacl.Secure(keyPath); err != nil {
			return "", "", fmt.Errorf("refusing to reuse TLS key: %w", err)
		}
		if err := winacl.Secure(certPath); err != nil {
			return "", "", fmt.Errorf("refusing to reuse TLS certificate: %w", err)
		}
		return certPath, keyPath, nil
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}

	dnsNames := []string{"localhost"}
	ipAddrs := []net.IP{net.ParseIP("127.0.0.1")}
	for _, san := range extraSANs {
		if ip := net.ParseIP(san); ip != nil {
			ipAddrs = append(ipAddrs, ip)
		} else {
			dnsNames = append(dnsNames, san)
		}
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "eightly-agent"},
		NotBefore:             now,
		NotAfter:              now.Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		DNSNames:              dnsNames,
		IPAddresses:           ipAddrs,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", err
	}
	if _, err := winacl.ProtectDir(dir); err != nil {
		return "", "", fmt.Errorf("cannot restrict access to %s: %w", dir, err)
	}
	if err := writePEM(certPath, 0o644, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return "", "", err
	}
	if err := writePEM(keyPath, 0o600, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

// writePEM creates path empty, closes it to everyone but the agent on Windows,
// and only then writes the block, so the key is never on disk with the
// ProgramData ACL it would otherwise inherit.
func writePEM(path string, mode os.FileMode, block *pem.Block) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode) // #nosec G304 -- path is derived from admin-configured dir flag, not user input
	if err != nil {
		return err
	}
	if err := winacl.Secure(path); err != nil {
		_ = f.Close()
		return err
	}
	if err := pem.Encode(f, block); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// tightenKeyPerm repairs a reused private-key file whose mode is broader than
// 0600. On unix it chmods back to 0600; if that fails it refuses to reuse the
// key. The mode check is skipped on platforms without meaningful POSIX bits.
func tightenKeyPerm(keyPath string) error {
	fi, err := os.Stat(keyPath)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		// POSIX mode bits are synthesized from the read-only attribute on
		// Windows; winacl.Secure enforces the ACL there instead.
		return nil
	}
	if perm := fi.Mode().Perm(); perm&^0o600 != 0 {
		if err := os.Chmod(keyPath, 0o600); err != nil {
			return fmt.Errorf("refusing to reuse TLS key %s: mode %04o is broader than 0600 and could not be tightened: %w", keyPath, perm, err)
		}
	}
	return nil
}
