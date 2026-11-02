//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// fakeEOS stands in for the agent routes of an Eight.ly OS server
// (internal/agent in the eightly-os repo): register, re-register and
// heartbeat, with the same request and response shapes, status codes and
// token format. Tokens are base64url(claims) + "." + base64url(HMAC-SHA256),
// claims {sub, iss: "eightly-server", type: "agent", iat, exp}, signed with
// the fleet's shared secret.
type fakeEOS struct {
	secret string
	// ttl is the lifetime of the tokens it hands the agent. The real server
	// uses 30 days; a short one makes the agent renew within the test.
	ttl time.Duration

	mu            sync.Mutex
	enrollTokens  map[string]bool
	agents        map[string]bool
	registrations []registration
	heartbeats    []heartbeat
	enrolled      chan string
}

type registration struct {
	AgentID string
	Renewal bool   // authorised by the agent's own token, not an enrollment token
	Bearer  string // the token presented, for a renewal
	Token   string // the token handed back
	Port    string // the port the agent reported
	Docker  string // the docker_version the agent reported
}

type heartbeat struct {
	AgentID string
	Token   string
}

func newFakeEOS(secret, enrollToken string, ttl time.Duration) *fakeEOS {
	return &fakeEOS{
		secret:       secret,
		ttl:          ttl,
		enrollTokens: map[string]bool{enrollToken: true},
		agents:       map[string]bool{},
		enrolled:     make(chan string, 1),
	}
}

type tokenClaims struct {
	Sub  string `json:"sub"`
	Iss  string `json:"iss"`
	Type string `json:"type"`
	Iat  int64  `json:"iat"`
	Exp  int64  `json:"exp,omitempty"`
}

func signToken(secret string, c tokenClaims) string {
	payload, _ := json.Marshal(c)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// agentToken is what the server signs for agentID: valid from now for ttl.
func agentToken(secret, agentID string, ttl time.Duration) string {
	now := time.Now()
	return signToken(secret, tokenClaims{
		Sub: agentID, Iss: "eightly-server", Type: "agent",
		Iat: now.Unix(), Exp: now.Add(ttl).Unix(),
	})
}

// verify mirrors VerifyAgentToken on the server: signature, then lifetime.
func (f *fakeEOS) verify(token string) (tokenClaims, bool) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return tokenClaims{}, false
	}
	mac := hmac.New(sha256.New, []byte(f.secret))
	mac.Write([]byte(parts[0]))
	if !hmac.Equal([]byte(parts[1]), []byte(base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))) {
		return tokenClaims{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return tokenClaims{}, false
	}
	var c tokenClaims
	if json.Unmarshal(payload, &c) != nil || c.Exp == 0 || !time.Now().Before(time.Unix(c.Exp, 0)) {
		return tokenClaims{}, false
	}
	return c, true
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if t, ok := strings.CutPrefix(h, "Bearer "); ok {
		return t
	}
	return ""
}

type response struct {
	Success bool              `json:"success"`
	Data    map[string]string `json:"data,omitempty"`
	Message string            `json:"message,omitempty"`
}

func reply(w http.ResponseWriter, status int, r response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(r)
}

func (f *fakeEOS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/agents/register" && r.Method == http.MethodPost:
		f.register(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/agents/") && strings.HasSuffix(r.URL.Path, "/heartbeat") && r.Method == http.MethodPut:
		f.heartbeat(w, r, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/agents/"), "/heartbeat"))
	default:
		http.Error(w, "Not found", http.StatusNotFound)
	}
}

func (f *fakeEOS) register(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Hostname      string `json:"hostname"`
		IP            string `json:"ip"`
		Port          string `json:"port"`
		DockerVersion string `json:"docker_version"`
		EnrollToken   string `json:"enroll_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		reply(w, http.StatusBadRequest, response{Message: "invalid JSON: " + err.Error()})
		return
	}
	if body.Hostname == "" || body.IP == "" {
		reply(w, http.StatusBadRequest, response{Message: "hostname and ip are required"})
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	enrolling := f.enrollTokens[body.EnrollToken]
	delete(f.enrollTokens, body.EnrollToken) // single-use

	renewingID := ""
	presented := bearer(r)
	if c, ok := f.verify(presented); ok && f.agents[c.Sub] {
		renewingID = c.Sub
	}
	if renewingID == "" && !enrolling {
		reply(w, http.StatusForbidden, response{Message: "valid enrollment token or agent token required"})
		return
	}

	reg := registration{Port: body.Port, Docker: body.DockerVersion}
	status := http.StatusOK
	if renewingID != "" {
		reg.AgentID, reg.Renewal, reg.Bearer = renewingID, true, presented
	} else {
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		reg.AgentID = "agent-" + hex.EncodeToString(b)
		f.agents[reg.AgentID] = true
		status = http.StatusCreated
	}
	reg.Token = agentToken(f.secret, reg.AgentID, f.ttl)
	f.registrations = append(f.registrations, reg)
	if !reg.Renewal {
		select {
		case f.enrolled <- reg.AgentID:
		default:
		}
	}
	reply(w, status, response{
		Success: true,
		Data:    map[string]string{"agent_id": reg.AgentID, "token": reg.Token, "server_version": "e2e"},
		Message: "agent registered",
	})
}

func (f *fakeEOS) heartbeat(w http.ResponseWriter, r *http.Request, id string) {
	c, ok := f.verify(bearer(r))
	if !ok || c.Sub != id {
		reply(w, http.StatusUnauthorized, response{Message: "valid agent token required"})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.agents[id] {
		reply(w, http.StatusNotFound, response{Message: "agent not found, re-register"})
		return
	}
	f.heartbeats = append(f.heartbeats, heartbeat{AgentID: id, Token: bearer(r)})
	reply(w, http.StatusOK, response{Success: true, Message: "heartbeat received", Data: map[string]string{"server_version": "e2e"}})
}

func (f *fakeEOS) snapshot() ([]registration, []heartbeat) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]registration(nil), f.registrations...), append([]heartbeat(nil), f.heartbeats...)
}
