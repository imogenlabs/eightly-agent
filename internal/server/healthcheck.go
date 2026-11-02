// SPDX-License-Identifier: Apache-2.0

package server

import (
	"bytes"
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
// The image used to run busybox `wget --spider https://...`, which
// forks an ssl_client helper. When the listener stopped answering, Docker
// killed wget at the 5s timeout, the helper was reparented to PID 1 (the agent,
// which never reaps), and one zombie was left every 10 seconds: 115,420 of them
// on a production host. This check starts no child process, so there is nothing to
// orphan, and it gives up on ctx's deadline rather than waiting to be killed.
//
// It accepts exactly the certificate at certPath, the one the listener loads,
// so it answers whether THIS agent is healthy rather than whether something on
// the port speaks TLS. The certificate's dates are not checked. A certificate
// it cannot read makes the check fail.
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

// pinnedTLSConfig accepts exactly the certificate in certPath and nothing else.
//
// It compares the bytes the listener presents with the bytes on disk instead of
// building a chain, because a chain check also checks the validity dates. The
// agent's certificate is self-signed for a year and reused for good (Eight.ly OS
// pins the whole certificate, so the agent must never swap it), and a dated
// check would turn a healthy agent unhealthy on its first birthday. A byte match
// is the stronger pin anyway: only the certificate the agent loads passes.
func pinnedTLSConfig(certPath, host string) (*tls.Config, error) {
	data, err := os.ReadFile(certPath) // #nosec G304 -- the agent's own certificate under its configured data directory
	if err != nil {
		return nil, fmt.Errorf("cannot read the agent's certificate: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%s holds no PEM certificate", certPath)
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return nil, fmt.Errorf("cannot parse the agent's certificate %s: %w", certPath, err)
	}
	want := block.Bytes
	return &tls.Config{
		// Go's chain and hostname checks are replaced, not dropped: the
		// presented leaf must be byte-for-byte the agent's own certificate.
		InsecureSkipVerify: true, // #nosec G402 -- verification is the exact byte match in VerifyPeerCertificate below
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 || !bytes.Equal(rawCerts[0], want) {
				return errors.New("the listener presented a certificate that is not this agent's")
			}
			return nil
		},
		ServerName: host,
		MinVersion: tls.VersionTLS12,
	}, nil
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
