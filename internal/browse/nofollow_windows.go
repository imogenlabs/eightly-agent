// SPDX-License-Identifier: Apache-2.0

//go:build windows

package browse

// syscallNoFollow is a no-op on Windows, which lacks O_NOFOLLOW. The agent is
// deployed on Linux; this keeps cross-builds compiling.
const syscallNoFollow = 0
