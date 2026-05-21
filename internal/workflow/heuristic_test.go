package workflow

import "testing"

func TestFailsHeuristic_NeoResultJSON_Fail(t *testing.T) {
	// The default review.md ships with this exact shape. Without this fix
	// the prose-only matcher missed it and a failing review never triggered
	// a retry.
	output := "Findings: panic on nil input.\n\n" +
		"```neo-result\n" +
		`{"status": "fail", "summary": "missing nil check in foo()"}` + "\n" +
		"```\n"
	if !failsHeuristic(output) {
		t.Fatal("expected failure to be detected from neo-result JSON")
	}
}

func TestFailsHeuristic_NeoResultJSON_Pass(t *testing.T) {
	output := "Looks good.\n\n```neo-result\n{\"status\": \"pass\"}\n```"
	if failsHeuristic(output) {
		t.Fatal("expected pass to NOT be flagged as failure")
	}
}

func TestFailsHeuristic_NeoResultPreemptsPoseMarkers(t *testing.T) {
	// Structured signal takes priority — prose mentioning "tests failed"
	// inside a passing report should not trigger retry.
	output := "I checked that no tests failed.\n```neo-result\n{\"status\":\"pass\"}\n```"
	if failsHeuristic(output) {
		t.Fatalf("structured pass should override prose markers, got fail")
	}
}

func TestFailsHeuristic_LegacyProseMarkersStillWork(t *testing.T) {
	// Older prompts without the structured block still produce a verdict.
	cases := []string{
		"Verdict: FAIL",
		"status: fail",
		"Result: Fail",
		"❌ build broken",
		"Blocking issues:\n - X",
		"tests failed in package foo",
	}
	for _, c := range cases {
		if !failsHeuristic(c) {
			t.Errorf("legacy marker missed: %q", c)
		}
	}
}

func TestFailsHeuristic_MalformedJSONFallsThroughToProse(t *testing.T) {
	// A neo-result block with bad JSON shouldn't crash and shouldn't be
	// treated as a verdict — we fall back to the prose matcher.
	output := "```neo-result\n{not json}\n```\ntests failed"
	if !failsHeuristic(output) {
		t.Fatal("expected fallback to legacy prose match")
	}
}

func TestFailsHeuristic_PassClearOutputReturnsFalse(t *testing.T) {
	if failsHeuristic("all good — nothing else to report") {
		t.Fatal("unrelated passing prose flagged as failure")
	}
}
