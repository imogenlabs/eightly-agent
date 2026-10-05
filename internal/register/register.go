// SPDX-License-Identifier: Apache-2.0

package register

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/smashingtags/eightly-agent/internal/metrics"
)

const (
	heartbeatInterval    = 30 * time.Second
	heartbeatMaxInterval = 300 * time.Second
	heartbeatMaxFailures = 3
)

// State holds the persistent registration state for this agent.
type State struct {
	AgentID      string `json:"agent_id"`
	Token        string `json:"token"`
	ServerURL    string `json:"server_url"`
	RegisteredAt string `json:"registered_at"`
	// ServerPin is the TOFU-captured server leaf SPKI SHA-256 fingerprint
	// ("sha256/<base64>"). Captured on first successful register and enforced on
	// every subsequent register/re-register/heartbeat dial. Empty when the
	// InsecureServerTLS escape hatch is in effect.
	ServerPin string `json:"server_pin,omitempty"`
}

// AgentInfo describes the agent's host environment for registration.
type AgentInfo struct {
	Hostname      string `json:"hostname"`
	IP            string `json:"ip"`
	Port          string `json:"port"`
	DockerVersion string `json:"docker_version"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	AgentVersion  string `json:"agent_version"`
}

// LoadState reads the registration state from disk, returning nil if the file does not exist.
//
// The state file holds the agent's long-lived bearer token, so before reading it
// we refuse to load when the file or its parent directory is owned by another
// user or is more permissive than 0600 (file) / 0700 (dir). A loose-permission
// state file is a credential-leak vector, so we fail closed rather than trust it.
func LoadState(path string) (*State, error) {
	if err := checkStatePerms(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from admin-configured --state-file flag, not user input
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// checkStatePerms enforces that the state file (0600) and its parent dir (0700)
// are not world/group accessible and are owned by the current uid. Missing file
// is fine — that is the unregistered case. The ownership check is a no-op on
// platforms that do not report a uid (e.g. Windows).
func checkStatePerms(path string) error {
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := assertPerm(path, fi, 0o600); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	di, err := os.Stat(dir)
	if err != nil {
		return err
	}
	return assertPerm(dir, di, 0o700)
}

func assertPerm(name string, fi os.FileInfo, maxMode os.FileMode) error {
	if permsEnforced {
		if perm := fi.Mode().Perm(); perm&^maxMode != 0 {
			return fmt.Errorf("refusing to load registration state: %s has mode %04o, broader than %04o", name, perm, maxMode)
		}
	}
	if uid, ok := fileUID(fi); ok && uid != os.Getuid() {
		return fmt.Errorf("refusing to load registration state: %s is owned by uid %d, not the agent uid %d", name, uid, os.Getuid())
	}
	return nil
}

func (s *State) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// DetectInfo probes the local system and Docker socket to build an AgentInfo.
func DetectInfo(socketPath, port, version, advertiseIP string) AgentInfo {
	info := AgentInfo{
		Port:         port,
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		AgentVersion: version,
	}

	info.Hostname, _ = os.Hostname()
	if advertiseIP != "" {
		info.IP = advertiseIP
	} else {
		info.IP = detectLocalIP()
	}
	info.DockerVersion = detectDockerVersion(socketPath)

	return info
}

func detectLocalIP() string {
	conn, err := net.DialTimeout("udp", "8.8.8.8:53", 3*time.Second)
	if err != nil {
		log.Debug().Err(err).Msg("UDP dial to 8.8.8.8:53 failed during local IP detection")
		return ""
	}
	defer conn.Close()
	addr := conn.LocalAddr().(*net.UDPAddr)
	return addr.IP.String()
}

func detectDockerVersion(socketPath string) string {
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		},
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://docker/version", nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return ""
	}
	var v struct {
		Version string `json:"Version"`
	}
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	return v.Version
}

// BuildServerTLSConfig creates a *tls.Config for connections to the EOS server.
//
// Trust model (in precedence order):
//   - insecure == true: the --insecure-server-tls escape hatch. Skip ALL
//     verification, including pinning. Loud warning. Never use in production.
//   - caPath set: verify the server chain against the supplied CA AND pin the
//     server leaf SPKI (TOFU). Defense in depth — a compromised/mis-issued cert
//     under the same CA still trips the pin.
//   - default (no CA): pin-only TOFU. The system trust store cannot vouch for a
//     self-signed appliance server, so chain verification is disabled and trust
//     rests entirely on the first-seen leaf SPKI fingerprint, persisted in the
//     registration state and enforced on every later dial.
//
// The returned config carries a pinHolder (via the package pin registry) that
// Register captures into and StartHeartbeat seeds from the persisted pin.
func BuildServerTLSConfig(caPath string, insecure bool, log zerolog.Logger) (*tls.Config, error) {
	if insecure {
		log.Warn().Msg("--insecure-server-tls set: skipping server cert verification AND TOFU pinning — DO NOT USE IN PRODUCTION")
		return &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, nil // #nosec G402 -- explicit opt-in via --insecure-server-tls
	}

	holder := &pinHolder{}
	cfg := &tls.Config{
		MinVersion:       tls.VersionTLS12,
		VerifyConnection: verifyPinned(holder),
	}

	if caPath != "" {
		caCert, err := os.ReadFile(caPath) // #nosec G304 -- path comes from admin-configured --server-ca flag, not user input
		if err != nil {
			return nil, fmt.Errorf("failed to read server CA cert %q: %w", caPath, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("server CA cert %q contains no valid PEM certificates", caPath)
		}
		// RootCAs set => Go performs full chain verification; VerifyConnection
		// then additionally enforces the leaf pin.
		cfg.RootCAs = pool
	} else {
		// Pin-only TOFU: disable chain verification so a self-signed server
		// leaf is reachable; the pin (not a CA) is the trust anchor.
		cfg.InsecureSkipVerify = true // #nosec G402 -- trust is enforced by the TOFU leaf-SPKI pin in VerifyConnection
	}

	pinRegistry.Store(cfg, holder)
	return cfg, nil
}

type registerPayload struct {
	AgentInfo
	EnrollToken string `json:"enroll_token,omitempty"`
}

// Register performs initial agent enrollment with the EOS server.
func Register(serverURL, enrollToken string, info AgentInfo, tlsCfg *tls.Config) (*State, error) {
	payload := registerPayload{
		AgentInfo:   info,
		EnrollToken: enrollToken,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
		},
	}

	req, err := http.NewRequest("POST", serverURL+"/api/agents/register", bytes.NewReader(body)) //nolint:noctx // Register() has no parent context; caller-provided timeout via http.Client
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if enrollToken != "" {
		req.Header.Set("Authorization", "Bearer "+enrollToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		metrics.RegistrationAttempts.WithLabelValues("error").Inc()
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		metrics.RegistrationAttempts.WithLabelValues("rejected").Inc()
		return nil, fmt.Errorf("register failed: status %d", resp.StatusCode)
	}

	var result struct {
		Success bool `json:"success"`
		Data    struct {
			AgentID string `json:"agent_id"`
			Token   string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		metrics.RegistrationAttempts.WithLabelValues("error").Inc()
		return nil, fmt.Errorf("register: bad response: %w", err)
	}
	if !result.Success {
		metrics.RegistrationAttempts.WithLabelValues("rejected").Inc()
		return nil, fmt.Errorf("register: server returned success=false")
	}

	metrics.RegistrationAttempts.WithLabelValues("success").Inc()
	return &State{
		AgentID:      result.Data.AgentID,
		Token:        result.Data.Token,
		ServerURL:    serverURL,
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
		// TOFU: the leaf SPKI observed during this (first) successful handshake
		// is now the pin. Persisting it lets every later dial enforce it.
		ServerPin: currentPin(tlsCfg),
	}, nil
}

type reRegisterPayload struct {
	AgentInfo
	AgentID string `json:"agent_id"`
}

// ReRegister re-enrolls an already-registered agent after repeated heartbeat failures.
//
// It reuses the same pinned tlsCfg as the original registration, so the TOFU
// server-cert pin is enforced here too — re-registration never weakens the
// transport trust. The agent re-authenticates with its existing bearer token;
// rotating or expiring that token is a server-side responsibility (the agent
// holds a long-lived token and has no protocol affordance to rotate it
// unilaterally). Residual token-lifecycle hardening is tracked server-side.
func ReRegister(state *State, info AgentInfo, tlsCfg *tls.Config) (*State, error) {
	payload := reRegisterPayload{
		AgentInfo: info,
		AgentID:   state.AgentID,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
		},
	}

	req, err := http.NewRequest("POST", state.ServerURL+"/api/agents/register", bytes.NewReader(body)) //nolint:noctx // ReRegister() has no parent context; caller-provided timeout via http.Client
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+state.Token)

	resp, err := client.Do(req)
	if err != nil {
		metrics.RegistrationAttempts.WithLabelValues("error").Inc()
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		metrics.RegistrationAttempts.WithLabelValues("rejected").Inc()
		return nil, fmt.Errorf("re-register failed: status %d", resp.StatusCode)
	}

	var result struct {
		Success bool `json:"success"`
		Data    struct {
			AgentID string `json:"agent_id"`
			Token   string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		metrics.RegistrationAttempts.WithLabelValues("error").Inc()
		return nil, fmt.Errorf("re-register: bad response: %w", err)
	}
	if !result.Success {
		metrics.RegistrationAttempts.WithLabelValues("rejected").Inc()
		return nil, fmt.Errorf("re-register: server returned success=false")
	}

	metrics.RegistrationAttempts.WithLabelValues("success").Inc()
	pin := currentPin(tlsCfg)
	if pin == "" {
		// Carry the previously-persisted pin forward if the config holder was
		// empty (e.g. a code path that didn't seed it).
		pin = state.ServerPin
	}
	return &State{
		AgentID:      result.Data.AgentID,
		Token:        result.Data.Token,
		ServerURL:    state.ServerURL,
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
		ServerPin:    pin,
	}, nil
}

// StartHeartbeat sends periodic heartbeats to the EOS server, re-registering on repeated failures.
func StartHeartbeat(ctx context.Context, state *State, statePath, socketPath, version, advertiseIP string, tlsCfg *tls.Config, log zerolog.Logger) {
	// Seed the TOFU pin from persisted state so that, across a process restart,
	// the very first heartbeat/re-register dial enforces the saved pin instead
	// of blindly re-capturing whatever cert is presented. server.go builds the
	// tls.Config before loading state, so this is where the two meet.
	seedPin(tlsCfg, state.ServerPin)

	h := newHeartbeater(state, statePath, socketPath, version, advertiseIP, tlsCfg, log)
	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("heartbeat stopped")
			return
		case <-time.After(h.interval):
		}
		h.step(ctx)
	}
}

// heartbeater is one agent's heartbeat state between beats. StartHeartbeat
// owns the timing; step is one beat, so a test can drive months of them.
type heartbeater struct {
	state                                       *State
	statePath, socketPath, version, advertiseIP string
	tlsCfg                                      *tls.Config
	log                                         zerolog.Logger
	interval                                    time.Duration
	failures                                    int
	toldExpired                                 bool
	// unsaved is true while the in-memory state holds a renewed token that
	// has not reached disk; every beat tries again until it does.
	unsaved bool
}

func newHeartbeater(state *State, statePath, socketPath, version, advertiseIP string, tlsCfg *tls.Config, log zerolog.Logger) *heartbeater {
	return &heartbeater{
		state: state, statePath: statePath, socketPath: socketPath, version: version,
		advertiseIP: advertiseIP, tlsCfg: tlsCfg, log: log, interval: heartbeatInterval,
	}
}

func (h *heartbeater) step(ctx context.Context) {
	if h.unsaved {
		h.save()
	}
	lt, known := readTokenLifetime(h.state.Token)
	err := sendHeartbeat(ctx, h.state, h.tlsCfg)
	if err != nil && known && lt.expired(clock()) {
		// EOS-2536: the server refuses an expired token on heartbeat AND on
		// register, so re-registering with it can only ever be refused. Say
		// once what fixes it and wait at the slowest pace; a beat still goes
		// out in case this host's clock is the thing that is wrong.
		if !h.toldExpired {
			h.log.Error().Time("expired_at", lt.expires).Msg("agent token expired, so this agent must be re-enrolled: generate an install command on the Agents page and run it on this host")
			h.toldExpired = true
		}
		h.interval = heartbeatMaxInterval
		return
	}
	if err != nil {
		h.failures++
		metrics.HeartbeatsFailed.WithLabelValues("send_error").Inc()
		h.log.Warn().Err(err).Int("failures", h.failures).Msg("heartbeat failed")

		h.interval *= 2
		if h.interval > heartbeatMaxInterval {
			h.interval = heartbeatMaxInterval
		}

		if h.failures >= heartbeatMaxFailures {
			h.log.Warn().Msg("re-registering after repeated heartbeat failures")
			if h.reRegister() {
				h.failures = 0
				h.interval = heartbeatInterval
				h.log.Info().Str("agent_id", h.state.AgentID).Msg("re-registered")
			}
		}
		return
	}

	metrics.HeartbeatsSent.Inc()
	h.failures = 0
	h.interval = heartbeatInterval
	h.toldExpired = false
	h.log.Debug().Str("agent_id", h.state.AgentID).Msg("heartbeat ok")

	// EOS-2536: tokens live 30 days and the only renewal used to be the
	// re-register after three failed beats, by which time the token had
	// lapsed and the server refused it. Renew from half-life on, with the
	// token still valid; a failure is retried on the next beat.
	if known && lt.renewDue(clock()) && h.reRegister() {
		if nl, ok := readTokenLifetime(h.state.Token); ok {
			h.log.Info().Time("expires_at", nl.expires).Msg("renewed agent token")
		}
	}
}

// reRegister trades the current token for a new one and saves it.
func (h *heartbeater) reRegister() bool {
	info := DetectInfo(h.socketPath, "", h.version, h.advertiseIP)
	newState, err := ReRegister(h.state, info, h.tlsCfg)
	if err != nil {
		h.log.Error().Err(err).Msg("re-registration failed")
		return false
	}
	*h.state = *newState
	h.save()
	return true
}

// save writes the state to disk. A failure leaves it pending for the next
// beat: a renewed token that only lives in memory is lost on restart, and the
// one on disk lapses.
func (h *heartbeater) save() {
	if err := h.state.Save(h.statePath); err != nil {
		h.unsaved = true
		h.log.Error().Err(err).Msg("failed to save new state; will retry on the next heartbeat")
		return
	}
	if h.unsaved {
		h.log.Info().Msg("saved the renewed agent token")
	}
	h.unsaved = false
}

func sendHeartbeat(ctx context.Context, state *State, tlsCfg *tls.Config) error {
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
		},
	}
	url := fmt.Sprintf("%s/api/agents/%s/heartbeat", state.ServerURL, state.AgentID)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+state.Token)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("heartbeat: %d", resp.StatusCode)
	}
	return nil
}
