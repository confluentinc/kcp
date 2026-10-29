package plan

import (
	"reflect"
	"testing"
)

// A declared question's answer must survive a set -> engineValues round-trip;
// otherwise it applies on the current run but silently resets on the regenerated
// plan-inputs.yaml (the class of bug the gov question hit). This guards every
// non-scan catalog question against a missing engineValues case.
func TestCatalog_EveryQuestionRoundTripsThroughEngineValues(t *testing.T) {
	for _, q := range catalog {
		if q.Scan {
			continue
		}
		var sample string
		for _, o := range q.Opts {
			if o.Engine != "" {
				sample = o.Engine
				break
			}
		}
		if sample == "" {
			continue // no non-empty engine value to exercise
		}
		if q.set == nil {
			t.Errorf("question %q has no set func", q.Key)
			continue
		}
		var in IntakeInputs
		q.set(&in, []string{sample})
		got := q.engineValues(in)
		if len(got) == 0 || got[0] != sample {
			t.Errorf("question %q does not round-trip through engineValues: set %q, engineValues returned %v (missing a case?)", q.Key, sample, got)
		}
	}
}

// Every app-scoped key must be a real catalog question.
func TestAppQuestionKeys_AllInCatalog(t *testing.T) {
	known := map[string]bool{}
	for _, q := range catalog {
		known[q.Key] = true
	}
	for k := range appQuestionKeys {
		if !known[k] {
			t.Errorf("appQuestionKeys has %q, which is not a catalog question", k)
		}
	}
}

// mergeInputs must propagate every field of IntakeInputs; a field added to the struct
// but forgotten in mergeInputs would silently fall to zero for every override.
func TestMergeInputs_CoversAllFields(t *testing.T) {
	var over IntakeInputs
	rv := reflect.ValueOf(&over).Elem()
	for i := 0; i < rv.NumField(); i++ {
		f := rv.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString("x")
		case reflect.Slice:
			f.Set(reflect.ValueOf([]string{"x"}))
		default:
			t.Fatalf("IntakeInputs field %s has unhandled kind %s — extend this test", rv.Type().Field(i).Name, f.Kind())
		}
	}
	got := mergeInputs(IntakeInputs{}, over)
	if !reflect.DeepEqual(got, over) {
		t.Errorf("mergeInputs dropped a field:\n got  %+v\n want %+v", got, over)
	}
}
