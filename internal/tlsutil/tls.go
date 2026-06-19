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
)

// EnsureCert returns paths to a TLS cert and key in dir, generating a self-signed pair if they don't exist.
func EnsureCert(dir string, extraSANs ...string) (certPath, keyPath string, err error) {
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")

	if fileExists(certPath) && fileExists(keyPath) {
		// Reuse path: the private key must not be group/world-readable. Tighten
		// it back to 0600 and warn rather than serving with a leaked key.
		if err := tightenKeyPerm(keyPath); err != nil {
			return "", "", err
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

	certFile, err := os.Create(certPath) // #nosec G304 -- path is derived from admin-configured dir flag, not user input
	if err != nil {
		return "", "", err
	}
	defer certFile.Close()
	pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}) // #nosec G104 -- file already opened; write failure surfaces as TLS load error

	keyFile, err := os.OpenFile(keyPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600) // #nosec G304 -- path is derived from admin-configured dir flag, not user input
	if err != nil {
		return "", "", err
	}
	defer keyFile.Close()
	pem.Encode(keyFile, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}) // #nosec G104 -- file already opened; write failure surfaces as TLS load error

	return certPath, keyPath, nil
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
		// Windows; the 0600 check is not meaningful there.
		return nil
	}
	if perm := fi.Mode().Perm(); perm&^0o600 != 0 {
		if err := os.Chmod(keyPath, 0o600); err != nil {
			return fmt.Errorf("refusing to reuse TLS key %s: mode %04o is broader than 0600 and could not be tightened: %w", keyPath, perm, err)
		}
	}
	return nil
}
