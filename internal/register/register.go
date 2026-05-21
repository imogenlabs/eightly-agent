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
func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
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
	resp, err := client.Get("http://docker/version")
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
// If caPath is set, the CA cert is loaded into a custom RootCAs pool.
// If insecure is true, InsecureSkipVerify is used.
// If neither is set, InsecureSkipVerify defaults to true for backwards
// compatibility, and a warning is logged.
func BuildServerTLSConfig(caPath string, insecure bool, log zerolog.Logger) (*tls.Config, error) {
	if caPath != "" {
		caCert, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read server CA cert %q: %w", caPath, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("server CA cert %q contains no valid PEM certificates", caPath)
		}
		return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, nil
	}
	if insecure {
		return &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, nil // #nosec G402 -- explicit opt-in via --insecure-server-tls
	}
	log.Warn().Msg("no --server-ca or --insecure-server-tls set; defaulting to InsecureSkipVerify=true (set --server-ca for production)")
	return &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, nil // #nosec G402 -- backwards-compat default, warns at startup
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

	req, err := http.NewRequest("POST", serverURL+"/api/agents/register", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if enrollToken != "" {
		req.Header.Set("Authorization", "Bearer "+enrollToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("register failed: %d %s", resp.StatusCode, string(raw))
	}

	var result struct {
		Success bool `json:"success"`
		Data    struct {
			AgentID string `json:"agent_id"`
			Token   string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("register: bad response: %w", err)
	}
	if !result.Success {
		return nil, fmt.Errorf("register: server returned success=false")
	}

	return &State{
		AgentID:      result.Data.AgentID,
		Token:        result.Data.Token,
		ServerURL:    serverURL,
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

type reRegisterPayload struct {
	AgentInfo
	AgentID string `json:"agent_id"`
}

// ReRegister re-enrolls an already-registered agent after repeated heartbeat failures.
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

	req, err := http.NewRequest("POST", state.ServerURL+"/api/agents/register", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+state.Token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("re-register failed: %d %s", resp.StatusCode, string(raw))
	}

	var result struct {
		Success bool `json:"success"`
		Data    struct {
			AgentID string `json:"agent_id"`
			Token   string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("re-register: bad response: %w", err)
	}
	if !result.Success {
		return nil, fmt.Errorf("re-register: server returned success=false")
	}

	return &State{
		AgentID:      result.Data.AgentID,
		Token:        result.Data.Token,
		ServerURL:    state.ServerURL,
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// StartHeartbeat sends periodic heartbeats to the EOS server, re-registering on repeated failures.
func StartHeartbeat(ctx context.Context, state *State, statePath, socketPath, version, advertiseIP string, tlsCfg *tls.Config, log zerolog.Logger) {
	interval := heartbeatInterval
	maxInterval := heartbeatMaxInterval
	failures := 0

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("heartbeat stopped")
			return
		case <-time.After(interval):
		}

		err := sendHeartbeat(ctx, state, tlsCfg)
		if err != nil {
			failures++
			log.Warn().Err(err).Int("failures", failures).Msg("heartbeat failed")

			interval = interval * 2
			if interval > maxInterval {
				interval = maxInterval
			}

			if failures >= heartbeatMaxFailures {
				log.Warn().Msg("re-registering after repeated heartbeat failures")
				info := DetectInfo(socketPath, "", version, advertiseIP)
				newState, regErr := ReRegister(state, info, tlsCfg)
				if regErr != nil {
					log.Error().Err(regErr).Msg("re-registration failed")
					continue
				}
				*state = *newState
				if saveErr := state.Save(statePath); saveErr != nil {
					log.Error().Err(saveErr).Msg("failed to save new state")
				}
				failures = 0
				interval = heartbeatInterval
				log.Info().Str("agent_id", state.AgentID).Msg("re-registered")
			}
			continue
		}

		failures = 0
		interval = heartbeatInterval
		log.Debug().Str("agent_id", state.AgentID).Msg("heartbeat ok")
	}
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
