// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
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
func Healthcheck(ctx context.Context, addr, port string) error {
	url := "https://" + net.JoinHostPort(healthcheckHost(addr), port) + "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	transport := &http.Transport{
		// The listener serves the agent's own self-signed certificate; this asks
		// whether it answers, not who it is.
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, // #nosec G402 -- loopback liveness probe of the agent's own self-signed listener
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
