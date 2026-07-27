//go:build !unix

package report

import "io/fs"

// checkAuditFilePerm is a no-op on platforms that do not carry a unix permission triple
// — notably Windows, which kcp ships a binary for.
//
// Go does not read a real mode there. It synthesises one from a single attribute bit
// (os/types_windows.go): every writable file reports 0666 and every read-only one 0444.
// os.OpenFile(..., 0600) does not set FILE_ATTRIBUTE_READONLY, so the file the audit
// writer JUST CREATED ITSELF stats back as 0666, and applying the unix assertion to that
// (0666 &^ 0600 = 0066) refused on the first run of every Windows invocation — telling
// the operator their brand-new file was "wider than 0600" and failing a command whose
// audit log is on by default.
//
// Widening this to "accept 0666 too" would be worse than a no-op: it would silently
// accept a genuinely world-writable file anywhere the split is later reused. Perm()
// simply carries no information on these platforms, so there is nothing to assert.
//
// What still holds everywhere is the IsRegular() check in openAuditFile, which stays
// outside this split, plus rotateExisting's Lstat classification. The confidentiality of
// the file on Windows rests on the directory's ACL, which is the platform's own model.
func checkAuditFilePerm(string, fs.FileMode) error { return nil }
