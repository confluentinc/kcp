package reconcile

import (
	"reflect"
	"testing"
)

func TestExplodeUnionDedupSort(t *testing.T) {
	source := []string{"team-a.orders", "team-a.payments", "billing-v2", "other"}
	got, err := Explode([]string{"billing-v2", "team-a.orders"}, []string{"team-a.*"}, source)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"billing-v2", "team-a.orders", "team-a.payments"} // sorted, deduped (team-a.orders once)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Explode = %v, want %v", got, want)
	}
}

func TestExplodeAnchored(t *testing.T) {
	source := []string{"orders", "my-orders", "orders-v2"}
	got, _ := Explode(nil, []string{"orders"}, source)
	if !reflect.DeepEqual(got, []string{"orders"}) {
		t.Fatalf("anchored pattern must full-match only 'orders', got %v", got)
	}
}

func TestExplodeBadPattern(t *testing.T) {
	if _, err := Explode(nil, []string{"team-a("}, []string{"x"}); err == nil {
		t.Fatal("an uncompilable pattern must return an error")
	}
}
