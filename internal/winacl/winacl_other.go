//go:build !windows

// SPDX-License-Identifier: Apache-2.0

package winacl

// Protect does nothing outside Windows: unix permission bits are enforced by
// the callers.
func Protect(string) error { return nil }

// Check does nothing outside Windows.
func Check(string) error { return nil }

// CheckOwner does nothing outside Windows.
func CheckOwner(string) error { return nil }
