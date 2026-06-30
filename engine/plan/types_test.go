package plan

import "testing"

// bugfixPlan builds the paper's motivating example (section 3.6): the 10-node
// "fix a Python auth bug and update docs" DAG. Two parallel searches, two
// parallel reads, an all_of analyze, two any_of patch alternatives + parallel
// docs, then tests and report.
func bugfixPlan() *Plan {
	node := func(id string, join JoinMode, se SideEffectLevel) Node {
		return Node{ID: id, ActionRef: id, Join: join, SideEffect: se, RetryBudget: 2, TimeoutMS: 60000}
	}
	return &Plan{
		ID:      "bugfix-auth",
		Version: 1,
		Nodes: []Node{
			node("search_auth", JoinAllOf, SideEffectRead),
			node("search_utils", JoinAllOf, SideEffectRead),
			node("read_auth", JoinAllOf, SideEffectRead),
			node("read_utils", JoinAllOf, SideEffectRead),
			node("analyze", JoinAllOf, SideEffectNone),
			node("fix_A", JoinAnyOf, SideEffectWrite),
			node("fix_B", JoinAnyOf, SideEffectWrite),
			node("update_docs", JoinAllOf, SideEffectWrite),
			node("run_tests", JoinAnyOf, SideEffectRead),
			node("report", JoinAllOf, SideEffectNone),
		},
		Edges: []Edge{
			{"search_auth", "read_auth"},
			{"search_utils", "read_utils"},
			{"read_auth", "analyze"},
			{"read_utils", "analyze"},
			{"analyze", "fix_A"},
			{"analyze", "fix_B"},
			{"analyze", "update_docs"},
			{"fix_A", "run_tests"},
			{"fix_B", "run_tests"},
			{"run_tests", "report"},
			{"update_docs", "report"},
		},
	}
}

func TestRoundTrip(t *testing.T) {
	p := bugfixPlan()
	b, err := p.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	got, err := FromJSON(b)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	b2, err := got.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON (round-trip): %v", err)
	}
	if string(b) != string(b2) {
		t.Fatalf("round-trip mismatch:\n--- before ---\n%s\n--- after ---\n%s", b, b2)
	}
}

func TestBasicValidate_OK(t *testing.T) {
	if err := bugfixPlan().BasicValidate(); err != nil {
		t.Fatalf("expected valid plan, got error: %v", err)
	}
}

func TestBasicValidate_Failures(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Plan)
	}{
		{"empty id", func(p *Plan) { p.ID = "" }},
		{"version zero", func(p *Plan) { p.Version = 0 }},
		{"no nodes", func(p *Plan) { p.Nodes = nil }},
		{"empty node id", func(p *Plan) { p.Nodes[0].ID = "" }},
		{"duplicate node id", func(p *Plan) { p.Nodes[1].ID = p.Nodes[0].ID }},
		{"invalid join", func(p *Plan) { p.Nodes[0].Join = "first_of" }},
		{"invalid side effect", func(p *Plan) { p.Nodes[0].SideEffect = "nuke" }},
		{"negative retry budget", func(p *Plan) { p.Nodes[0].RetryBudget = -1 }},
		{"dangling edge from", func(p *Plan) { p.Edges[0].From = "ghost" }},
		{"dangling edge to", func(p *Plan) { p.Edges[0].To = "ghost" }},
		{"self loop", func(p *Plan) { p.Edges = append(p.Edges, Edge{"analyze", "analyze"}) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := bugfixPlan()
			c.mutate(p)
			if err := p.BasicValidate(); err == nil {
				t.Fatalf("expected error for %q, got nil", c.name)
			}
		})
	}
}

func TestNodeState_IsTerminal(t *testing.T) {
	terminal := []NodeState{StateExecuted, StateFailed, StateCancelled, StateSkipped}
	nonTerminal := []NodeState{StatePending, StateReady, StateRunning, StateWaitingHuman, StateBlocked, StateFailedRetry}
	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("%q should be terminal", s)
		}
	}
	for _, s := range nonTerminal {
		if s.IsTerminal() {
			t.Errorf("%q should NOT be terminal", s)
		}
	}
}

func TestNodeState_IsTerminalSuccess(t *testing.T) {
	if !StateExecuted.IsTerminalSuccess() || !StateSkipped.IsTerminalSuccess() {
		t.Error("executed and skipped must be terminal-success")
	}
	for _, s := range []NodeState{StateFailed, StateCancelled, StateRunning} {
		if s.IsTerminalSuccess() {
			t.Errorf("%q must not be terminal-success", s)
		}
	}
}

func TestEnumValidity(t *testing.T) {
	if !JoinAllOf.Valid() || !JoinAnyOf.Valid() || JoinMode("first_of").Valid() {
		t.Error("JoinMode.Valid() is wrong")
	}
	for _, s := range []SideEffectLevel{SideEffectNone, SideEffectRead, SideEffectWrite, SideEffectDestructive} {
		if !s.Valid() {
			t.Errorf("%q should be a valid side-effect level", s)
		}
	}
	if SideEffectLevel("nuke").Valid() {
		t.Error("invalid side-effect level reported valid")
	}
}
