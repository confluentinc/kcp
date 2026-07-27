//go:build unix

package report

import "io/fs"

// checkAuditFilePerm rejects an audit log whose mode is wider than auditFileMode.
//
// auditFileMode is only applied when the open CREATES the file. An existing file keeps
// whatever mode it already had, so the mode has to be verified rather than assumed —
// the audit log carries topic names and transactional ids.
func checkAuditFilePerm(path string, mode fs.FileMode) error {
	if perm := mode.Perm(); perm&^auditFileMode != 0 {
		return errAuditFileTooWide(path, perm)
	}
	return nil
}
