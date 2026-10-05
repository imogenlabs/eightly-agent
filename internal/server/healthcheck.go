// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
)

// Healthcheck asks this agent's own HTTPS listener for /healthz and returns nil
// only on a 200. It is what the image's HEALTHCHECK runs (`eightly-agent
// healthcheck`), and it does the request in-process on purpose.
//
// EOS-2533: the image used to run busybox `wget --spider https://...`, which
// forks an ssl_client helper. When the listener stopped answering, Docker
// killed wget at the 5s timeout, the helper was reparented to PID 1 (the agent,
// which never reaps), and one zombie was left every 10 seconds: 115,420 of them
// on eightly-ms01. This check starts no child process, so there is nothing to
// orphan, and it gives up on ctx's deadline rather than waiting to be killed.
//
// It trusts exactly the certificate at certPath, the one the listener loads, so
// it answers whether THIS agent is healthy rather than whether something on the
// port speaks TLS. A certificate it cannot read makes the check fail.
func Healthcheck(ctx context.Context, addr, port, certPath string) error {
	host := healthcheckHost(addr)
	tlsConfig, err := pinnedTLSConfig(certPath, host)
	if err != nil {
		return err
	}
	url := "https://" + net.JoinHostPort(host, port) + "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	transport := &http.Transport{
		TLSClientConfig:   tlsConfig,
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d", url, resp.StatusCode)
	}
	return nil
}

// pinnedTLSConfig trusts only the certificate(s) in certPath. The agent's
// generated certificate is self-signed and not a CA; Go accepts such a leaf
// when that exact certificate is in RootCAs, so chain verification stays on.
//
// ServerName must be a name the certificate carries or verification fails on
// the name, not the key. The dialled host is used when the certificate covers
// it; otherwise the certificate's first SAN is. Trust is already pinned to this
// one certificate, so choosing the name from it gives nothing away.
func pinnedTLSConfig(certPath, host string) (*tls.Config, error) {
	data, err := os.ReadFile(certPath) // #nosec G304 -- the agent's own certificate under its configured data directory
	if err != nil {
		return nil, fmt.Errorf("cannot read the agent's certificate: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%s holds no PEM certificate", certPath)
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("cannot parse the agent's certificate %s: %w", certPath, err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(data)

	serverName, err := certServerName(leaf, host)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", certPath, err)
	}
	return &tls.Config{RootCAs: pool, ServerName: serverName, MinVersion: tls.VersionTLS12}, nil
}

func certServerName(leaf *x509.Certificate, host string) (string, error) {
	if leaf.VerifyHostname(host) == nil {
		return host, nil
	}
	if len(leaf.DNSNames) > 0 {
		return leaf.DNSNames[0], nil
	}
	if len(leaf.IPAddresses) > 0 {
		return leaf.IPAddresses[0].String(), nil
	}
	return "", errors.New("certificate names no host or IP address to verify it against")
}

// healthcheckHost turns the listen address into one a client can dial: a
// wildcard bind is reached on loopback, anything specific is used as given.
// IPv6 brackets are dropped because net.JoinHostPort adds its own.
func healthcheckHost(addr string) string {
	host := strings.Trim(addr, "[]")
	switch host {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	}
	return host
}
