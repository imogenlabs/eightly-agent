package register

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
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
)

type State struct {
	AgentID      string `json:"agent_id"`
	Token        string `json:"token"`
	ServerURL    string `json:"server_url"`
	RegisteredAt string `json:"registered_at"`
}

type AgentInfo struct {
	Hostname      string `json:"hostname"`
	IP            string `json:"ip"`
	Port          string `json:"port"`
	DockerVersion string `json:"docker_version"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	AgentVersion  string `json:"agent_version"`
}

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

func DetectInfo(socketPath, port, version string) AgentInfo {
	info := AgentInfo{
		Port:         port,
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		AgentVersion: version,
	}

	info.Hostname, _ = os.Hostname()
	info.IP = detectLocalIP()
	info.DockerVersion = detectDockerVersion(socketPath)

	return info
}

func detectLocalIP() string {
	conn, err := net.DialTimeout("udp", "8.8.8.8:53", 3*time.Second)
	if err != nil {
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

type registerPayload struct {
	Hostname      string `json:"hostname"`
	IP            string `json:"ip"`
	Port          string `json:"port"`
	DockerVersion string `json:"docker_version"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	AgentVersion  string `json:"agent_version"`
	EnrollToken   string `json:"enroll_token,omitempty"`
}

func Register(serverURL, enrollToken string, info AgentInfo) (*State, error) {
	payload := registerPayload{
		Hostname:      info.Hostname,
		IP:            info.IP,
		Port:          info.Port,
		DockerVersion: info.DockerVersion,
		OS:            info.OS,
		Arch:          info.Arch,
		AgentVersion:  info.AgentVersion,
		EnrollToken:   enrollToken,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
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
	if err := json.NewDecoder(bufio.NewReader(resp.Body)).Decode(&result); err != nil {
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

func StartHeartbeat(ctx context.Context, state *State, statePath, version string, log zerolog.Logger) {
	interval := 30 * time.Second
	maxInterval := 300 * time.Second
	failures := 0

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("heartbeat stopped")
			return
		case <-time.After(interval):
		}

		err := sendHeartbeat(state)
		if err != nil {
			failures++
			log.Warn().Err(err).Int("failures", failures).Msg("heartbeat failed")

			interval = interval * 2
			if interval > maxInterval {
				interval = maxInterval
			}

			if failures >= 3 {
				log.Warn().Msg("re-registering after repeated heartbeat failures")
				info := DetectInfo("", "", version)
				newState, regErr := Register(state.ServerURL, "", info)
				if regErr != nil {
					log.Error().Err(regErr).Msg("re-registration failed")
					continue
				}
				*state = *newState
				if saveErr := state.Save(statePath); saveErr != nil {
					log.Error().Err(saveErr).Msg("failed to save new state")
				}
				failures = 0
				interval = 30 * time.Second
				log.Info().Str("agent_id", state.AgentID).Msg("re-registered")
			}
			continue
		}

		failures = 0
		interval = 30 * time.Second
		log.Debug().Str("agent_id", state.AgentID).Msg("heartbeat ok")
	}
}

func sendHeartbeat(state *State) error {
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	url := fmt.Sprintf("%s/api/agents/%s/heartbeat", state.ServerURL, state.AgentID)
	req, err := http.NewRequest("PUT", url, nil)
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
