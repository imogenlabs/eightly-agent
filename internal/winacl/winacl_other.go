//go:build !windows

// SPDX-License-Identifier: Apache-2.0

package winacl

// Protect does nothing outside Windows: unix permission bits are enforced by
// the callers.
func Protect(string) error { return nil }

// Check does nothing outside Windows.
func Check(string) error { return nil }

// guardsFolders is false outside Windows: the unix mode and owner checks of
// each caller cover the folder.
const guardsFolders = false

func checkShared(string) error { return nil }

func checkAncestors(string) error { return nil }
