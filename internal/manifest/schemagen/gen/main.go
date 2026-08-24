// Command gen writes the committed JSON Schemas. Run via:
//
//	go generate ./internal/manifest/...
//
// It writes BOTH schemas from one invocation rather than taking a target
// argument, so the two can never be regenerated separately and drift apart.
package main

import (
	"os"

	"github.com/confluentinc/kcp/internal/manifest/schemagen"
)

func main() {
	// CWD is internal/manifest when invoked by the //go:generate directive there.
	write("migration.schema.json", schemagen.Generate)
	write("gatewaymigration.schema.json", schemagen.GenerateGateway)
}

func write(name string, gen func() ([]byte, error)) {
	b, err := gen()
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(name, b, 0o644); err != nil {
		panic(err)
	}
}
