//go:build !windows

// SPDX-License-Identifier: Apache-2.0

package register

// secureState is a no-op outside Windows: there checkStatePerms enforces the
// unix mode and owner of the state file and its folder.
func secureState(string) error { return nil }

// prepareStateWrite is a no-op outside Windows, where os.WriteFile's 0600
// already applies.
func prepareStateWrite(string) error { return nil }
