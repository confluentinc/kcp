package reconcile

import (
	"reflect"
	"testing"
)

func convertFenceEntry() map[string]any {
	return map[string]any{"topicPatterns": []any{".*"}, "blocked": true}
}

func TestPrependConvertFence_HeadsTheListAndKeepsOperatorEntries(t *testing.T) {
	operator := map[string]any{"trafficType": "TRANSACTION"}
	rt, _ := ParseRules(map[string]any{"fencing": []any{operator}})

	rt.PrependConvertFence()

	fencing, _ := rt.root["fencing"].([]any)
	if len(fencing) != 2 {
		t.Fatalf("fencing = %v, want convert fence + operator entry", fencing)
	}
	if !reflect.DeepEqual(fencing[0], convertFenceEntry()) {
		t.Fatalf("fencing[0] = %v, want kcp's convert fence", fencing[0])
	}
	if !reflect.DeepEqual(fencing[1], operator) {
		t.Fatalf("fencing[1] = %v, want the operator entry preserved", fencing[1])
	}
}

func TestPrependConvertFence_IsIdempotent(t *testing.T) {
	rt, _ := ParseRules(map[string]any{"fencing": []any{convertFenceEntry()}})

	rt.PrependConvertFence()

	fencing, _ := rt.root["fencing"].([]any)
	if len(fencing) != 1 {
		t.Fatalf("fencing = %v, want exactly one convert fence after a resume", fencing)
	}
}

func TestDropConvertFence_RemovesOnlyKcpsFence(t *testing.T) {
	operator := map[string]any{"topics": []any{"orders"}, "blocked": true, "trafficType": "PRODUCE"}
	rt, _ := ParseRules(map[string]any{"fencing": []any{convertFenceEntry(), operator}})

	rt.DropConvertFence()

	fencing, _ := rt.root["fencing"].([]any)
	if len(fencing) != 1 || !reflect.DeepEqual(fencing[0], operator) {
		t.Fatalf("fencing = %v, want only the operator entry", fencing)
	}
}

func TestHasConvertFence(t *testing.T) {
	fenced, _ := ParseRules(map[string]any{"fencing": []any{convertFenceEntry()}})
	if !fenced.HasConvertFence() {
		t.Fatal("HasConvertFence = false on a route carrying kcp's convert fence")
	}
	unfenced, _ := ParseRules(map[string]any{"fencing": []any{}})
	if unfenced.HasConvertFence() {
		t.Fatal("HasConvertFence = true on an unfenced route")
	}
}

func TestIsKcpConvertFence_RejectsLookalikes(t *testing.T) {
	cases := map[string]any{
		"extra key":         map[string]any{"topicPatterns": []any{".*"}, "blocked": true, "trafficType": "PRODUCE"},
		"not blocked":       map[string]any{"topicPatterns": []any{".*"}, "blocked": false},
		"different pattern": map[string]any{"topicPatterns": []any{"team-a.*"}, "blocked": true},
		"exact-topic fence": map[string]any{"topics": []any{"orders"}, "blocked": true},
		"not a map":         "fence",
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			if isKcpConvertFence(e) {
				t.Fatalf("isKcpConvertFence(%v) = true, want false", e)
			}
		})
	}
	readBack := map[string]any{"topicPatterns": []any{".*"}, "blocked": true, "topics": []any{}}
	if !isKcpConvertFence(readBack) {
		t.Fatal("an empty key read back from the live CR must not make kcp's fence someone else's")
	}
}

func TestForeignFences(t *testing.T) {
	operator := map[string]any{"topics": []any{"orders"}, "blocked": true}
	rt, _ := ParseRules(map[string]any{"fencing": []any{convertFenceEntry(), map[string]any{}, operator}})

	got := rt.ForeignFences()

	if len(got) != 1 || !reflect.DeepEqual(got[0], operator) {
		t.Fatalf("ForeignFences = %v, want only the operator entry (kcp's fence and the empty entry ignored)", got)
	}
}
