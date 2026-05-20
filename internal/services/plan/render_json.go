package plan

import (
	"encoding/json"
	"fmt"

	"github.com/confluentinc/kcp/internal/types"
)

// RenderJSON serialises a Plan to canonical 2-space-indented JSON. Stable
// key ordering comes for free from struct field order — any map members
// are encoded by Go's stdlib in sorted key order since Go 1.12.
func RenderJSON(p *types.Plan) ([]byte, error) {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render plan as JSON: %w", err)
	}
	return data, nil
}
