package omzet_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The open questions live beside the tax ones, in the directory that gets taken
// to the konsultan pajak (TASKS 5.6's practice, applied to Phase 7).
const unverifiedPath = "../../../testdata/worked_examples/omzet_unverified.json"

type openQuestions struct {
	About        string `json:"about"`
	Verification string `json:"verification"`
	Questions    []struct {
		Name    string `json:"name"`
		Affects string `json:"affects"`
		TODO    string `json:"todo"`
	} `json:"questions"`
}

// TestUnverifiedOmzetRulesAreRecorded prints what is not settled about the
// threshold, as one list.
//
// Every figure this package computes is arithmetic over a ledger and is tested
// to the rupiah. What is not settled is which regulation governs the two dates a
// crossing produces — and those dates are the feature. SPEC §5.2 cites a
// regulation about final PPh for a deadline about PPN registration, and the
// alternative reading is months earlier on the same facts.
//
// So both readings are implemented, the choice is a config row, and the question
// is recorded here rather than resolved by guessing. This test asserts the list
// exists and is answerable — it does not assert an answer.
func TestUnverifiedOmzetRulesAreRecorded(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(unverifiedPath)
	if err != nil {
		t.Fatalf("read %s: %v", unverifiedPath, err)
	}

	dec := json.NewDecoder(strings.NewReader(string(raw)))
	// A mistyped key would otherwise drop a question silently, which is the one
	// failure mode a list of open questions cannot have.
	dec.DisallowUnknownFields()

	var doc openQuestions
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("decode %s: %v", unverifiedPath, err)
	}
	if len(doc.Questions) == 0 {
		t.Fatal("no open questions recorded; if they were answered, the config rows should say so")
	}

	t.Logf("%d omzet rule(s) await verification against DJP sources and a konsultan pajak:",
		len(doc.Questions))
	for _, q := range doc.Questions {
		if q.TODO == "" || q.Affects == "" {
			t.Errorf("%q records no reasoning or does not say what it affects", q.Name)
		}
		// A question that does not say what to change when it is answered is a
		// note, not a task.
		if !strings.Contains(q.TODO, "WHAT TO ASK") {
			t.Errorf("%q does not say what to ask", q.Name)
		}
		t.Logf("  - %s\n      affects: %s", q.Name, q.Affects)
	}
}
