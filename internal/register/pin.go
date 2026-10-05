// SPDX-License-Identifier: Apache-2.0

package register

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"sync"
)

// pinHolder carries the TOFU server-cert pin across the lifetime of a single
// *tls.Config. The same config is reused for register, re-register, and every
// heartbeat dial, so the holder is the one place the pin is captured (on first
// connect) and enforced (on every connect thereafter).
//
// The holder is mutated from the TLS stack's VerifyConnection callback, which
// can run concurrently with the heartbeat goroutine seeding a persisted pin, so
// access is guarded by a mutex.
type pinHolder struct {
	mu  sync.Mutex
	pin string // SPKI SHA-256 fingerprint ("sha256/<base64>"), empty until captured
}

func (h *pinHolder) get() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.pin
}

// setIfEmpty records the pin on first observation (TOFU capture). It returns the
// effective pin: the previously-stored value if one already existed, otherwise
// the newly-captured value.
func (h *pinHolder) setIfEmpty(pin string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pin == "" {
		h.pin = pin
	}
	return h.pin
}

// pinRegistry associates each *tls.Config produced by BuildServerTLSConfig with
// its pinHolder. We cannot store user data on *tls.Config, and the config is the
// only object threaded through the (unchangeable) server.go call sites, so a
// pointer-keyed registry lets Register/StartHeartbeat reach the holder for the
// config they were handed.
var pinRegistry sync.Map // map[*tls.Config]*pinHolder

func holderFor(cfg *tls.Config) *pinHolder {
	if cfg == nil {
		return nil
	}
	if v, ok := pinRegistry.Load(cfg); ok {
		return v.(*pinHolder)
	}
	return nil
}

// seedPin injects a previously-persisted pin into the holder for cfg so that a
// process restart enforces the saved pin on the very first dial instead of
// blindly re-capturing (which would defeat TOFU across restarts).
func seedPin(cfg *tls.Config, pin string) {
	if pin == "" {
		return
	}
	if h := holderFor(cfg); h != nil {
		h.setIfEmpty(pin)
	}
}

// SeedServerPin holds cfg to a pin saved in an earlier registration before the
// state that carried it is discarded. Re-enrolling a lapsed agent with the same
// server (EOS-2536) must be held to the key it was pinned to, or the fresh
// single-use enrollment token goes to whatever certificate answers.
func SeedServerPin(cfg *tls.Config, pin string) { seedPin(cfg, pin) }

// currentPin returns the pin captured/enforced for cfg, or "" if none.
func currentPin(cfg *tls.Config) string {
	if h := holderFor(cfg); h != nil {
		return h.get()
	}
	return ""
}

// spkiFingerprint returns the base64 SHA-256 of the certificate's
// SubjectPublicKeyInfo, formatted as "sha256/<base64>". Pinning the SPKI (rather
// than the whole cert) survives benign certificate re-issuance that keeps the
// same key, which is the standard HPKP/RFC-7469 pin material.
func spkiFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return "sha256/" + base64.StdEncoding.EncodeToString(sum[:])
}

// verifyPinned is the VerifyConnection callback installed on pinned configs. It
// implements TOFU: capture the leaf SPKI on first connect, then require every
// subsequent connection's leaf SPKI to match.
func verifyPinned(h *pinHolder) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return fmt.Errorf("server presented no certificates")
		}
		leaf := spkiFingerprint(cs.PeerCertificates[0])
		effective := h.setIfEmpty(leaf)
		if effective != leaf {
			return fmt.Errorf("server cert pin mismatch: expected %s, got %s", effective, leaf)
		}
		return nil
	}
}
