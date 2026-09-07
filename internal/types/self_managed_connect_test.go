package types

import (
	"encoding/json"
	"testing"
)

func TestConnectClusterJSONRoundTrip(t *testing.T) {
	in := ConnectCluster{
		ConnectRestURL: "http://connect1:8083",
		Connectors: []Connector{
			{Name: "c1", State: "RUNNING", Config: map[string]any{"tasks.max": "1"}, ConnectHost: "10.0.0.1:8083"},
		},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out ConnectCluster
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ConnectRestURL != "http://connect1:8083" || len(out.Connectors) != 1 {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
	if out.Connectors[0].ConnectHost != "10.0.0.1:8083" || out.Connectors[0].Metrics != nil {
		t.Fatalf("connector fields wrong: %+v", out.Connectors[0])
	}
	// connect_rest_url must be the JSON key.
	if got := string(b); !json.Valid(b) || !contains(got, `"connect_rest_url":"http://connect1:8083"`) {
		t.Fatalf("json shape wrong: %s", got)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
