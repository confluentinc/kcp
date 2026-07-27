//go:build !unix

package report

import (
	"io/fs"
	"testing"
)

// The build tag is the same !unix one the production split uses, so this runs on the
// platforms the assertion is about. Windows itself cannot be executed here; GOOS=js
// GOARCH=wasm is a !unix platform that can, and the decision under test is a pure
// function of the mode, so running it there exercises the same branch Windows takes.

func TestCheckAuditFilePermAcceptsTheModeWindowsSynthesisesForAWritableFile(t *testing.T) {
	// Go does not read a real permission triple on Windows. It synthesises the mode from
	// a single attribute bit (os/types_windows.go): every writable file reports 0666 and
	// every read-only one 0444. os.OpenFile(..., 0600) does not set FILE_ATTRIBUTE_READONLY,
	// so the file the audit writer JUST CREATED ITSELF stats back as 0666.
	//
	// Applying the unix 0600 assertion to that turns 0666 &^ 0600 = 0066 into a refusal on
	// the FIRST run of every Windows invocation — kcp ships a Windows binary and the audit
	// log is on by default, so the command fails outright while telling the operator their
	// brand-new file is "wider than 0600". The mode is not expressible on this platform, so
	// Perm() carries no information to assert on and the check has to be a no-op.
	for _, mode := range []fs.FileMode{0666, 0444} {
		if err := checkAuditFilePerm("audit.log", mode); err != nil {
			t.Errorf("mode %#o: %v", mode, err)
		}
	}
}
