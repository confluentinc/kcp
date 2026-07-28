//go:build unix

package report

import "syscall"

// openNoFollow makes an open fail with ELOOP rather than follow a symbolic link at the
// final path component, closing the window between rotateExisting's check of the path
// and openAuditFile's open of it.
const openNoFollow = syscall.O_NOFOLLOW
