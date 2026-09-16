package engine

import "strings"

// driver names one input behind a verdict, tagged with where it came from.
type driver struct {
	source  string // "scan" | "answer"
	trigger string // short phrase, e.g. "partition count", "target authentication"
}

func sc(trigger string) driver  { return driver{"scan", trigger} }
func ans(trigger string) driver { return driver{"answer", trigger} }

// srcOr tags a driver as an answer when the fact was supplied by the customer
// (an override, or a value the scan never captured), otherwise as a scan finding.
// Used for the overridable scan facts (auth, tiered storage, connectors, topics)
// so their provenance reads correctly whichever way the value arrived.
func srcOr(answered bool, trigger string) driver {
	if answered {
		return ans(trigger)
	}
	return sc(trigger)
}

// basis renders a "Based on …," lead-in for a verdict's reason so the customer
// sees the source (their scan vs their answers) and the trigger behind the
// recommendation, e.g. "Based on your scan (partition count) and your answer
// (private networking required), ". The following clause is meant to continue
// lower-case ("… we recommend …"). Returns "" when no drivers are given, so a
// verdict with nothing concrete behind it reads as it did before.
func basis(ds ...driver) string {
	var scans, answers []string
	for _, d := range ds {
		switch d.source {
		case "scan":
			scans = appendUnique(scans, d.trigger)
		case "answer":
			answers = appendUnique(answers, d.trigger)
		}
	}
	var parts []string
	if len(scans) > 0 {
		parts = append(parts, "your scan ("+strings.Join(scans, ", ")+")")
	}
	if len(answers) > 0 {
		parts = append(parts, "your answer ("+strings.Join(answers, ", ")+")")
	}
	if len(parts) == 0 {
		return ""
	}
	return "Based on " + strings.Join(parts, " and ") + ", "
}

// lowerFirst lower-cases the first rune, so an existing sentence can follow a
// "Based on …, " lead as a continuation.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	// Leave all-caps acronyms (e.g. "PNI …") alone: only fold a leading capital
	// that's followed by a lower-case letter (an ordinary sentence opener).
	if len(r) > 1 && r[1] >= 'a' && r[1] <= 'z' {
		r[0] = []rune(strings.ToLower(string(r[0])))[0]
	}
	return string(r)
}

func appendUnique(xs []string, x string) []string {
	for _, e := range xs {
		if e == x {
			return xs
		}
	}
	return append(xs, x)
}
