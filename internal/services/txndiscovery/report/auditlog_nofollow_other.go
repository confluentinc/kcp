//go:build !unix

package report

// openNoFollow is a no-op flag on platforms whose syscall package does not define
// O_NOFOLLOW — notably Windows, which kcp ships a binary for. Referencing
// syscall.O_NOFOLLOW unconditionally would not compile there.
//
// The symlink defence on those platforms is rotateExisting's Lstat classification,
// which rejects anything that is not a regular file, plus openAuditFile's Fstat of the
// opened handle. Only the narrow race between those two is uncovered.
const openNoFollow = 0
