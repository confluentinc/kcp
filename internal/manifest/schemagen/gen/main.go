// Command gen writes the committed migration.schema.json. Run via:
//
//	go generate ./internal/manifest/...
package main

import (
	"os"

	"github.com/confluentinc/kcp/internal/manifest/schemagen"
)

func main() {
	b, err := schemagen.Generate()
	if err != nil {
		panic(err)
	}
	// CWD is internal/manifest when invoked by the //go:generate directive there.
	if err := os.WriteFile("migration.schema.json", b, 0o644); err != nil {
		panic(err)
	}
}
