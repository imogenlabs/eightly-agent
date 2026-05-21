// SPDX-License-Identifier: Apache-2.0

package tlsutil

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// EnsureCert returns paths to a TLS cert and key in dir, generating a self-signed pair if they don't exist.
func EnsureCert(dir string, extraSANs ...string) (certPath, keyPath string, err error) {
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")

	if fileExists(certPath) && fileExists(keyPath) {
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
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "eightly-agent"},
		NotBefore:    now,
		NotAfter:     now.Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
		IPAddresses:  ipAddrs,
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
