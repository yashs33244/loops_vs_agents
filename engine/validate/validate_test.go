package validate

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"sgh/engine/plan"
)

// bugfixPlan builds the paper's motivating example (section 3.6): the 10-node
// "fix a Python auth bug and update docs" DAG. Two parallel searches, two
// parallel reads, an all_of analyze, two patch alternatives + parallel docs,
// then an any_of run_tests and a final report. Mirrors the fixture in
// plan/types_test.go so the validator is exercised against the exact graph the
// scheduler will run.
//
// Join modes: any_of lives on the JOINING node and races its predecessors.
// run_tests is any_of over {fix_A, fix_B} (take whichever patch passes). fix_A,
// fix_B and report are plain all_of dependency joins.
func bugfixPlan() *plan.Plan {
	node := func(id string, join plan.JoinMode, se plan.SideEffectLevel) plan.Node {
		return plan.Node{ID: id, ActionRef: id, Join: join, SideEffect: se, RetryBudget: 2, TimeoutMS: 60000}
	}
	return &plan.Plan{
		ID:      "bugfix-auth",
		Version: 1,
		Nodes: []plan.Node{
			node("search_auth", plan.JoinAllOf, plan.SideEffectRead),
			node("search_utils", plan.JoinAllOf, plan.SideEffectRead),
			node("read_auth", plan.JoinAllOf, plan.SideEffectRead),
			node("read_utils", plan.JoinAllOf, plan.SideEffectRead),
			node("analyze", plan.JoinAllOf, plan.SideEffectNone),
			node("fix_A", plan.JoinAllOf, plan.SideEffectWrite),
			node("fix_B", plan.JoinAllOf, plan.SideEffectWrite),
			node("update_docs", plan.JoinAllOf, plan.SideEffectWrite),
			node("run_tests", plan.JoinAnyOf, plan.SideEffectRead),
			node("report", plan.JoinAllOf, plan.SideEffectNone),
		},
		Edges: []plan.Edge{
			{From: "search_auth", To: "read_auth"},
			{From: "search_utils", To: "read_utils"},
			{From: "read_auth", To: "analyze"},
			{From: "read_utils", To: "analyze"},
			{From: "analyze", To: "fix_A"},
			{From: "analyze", To: "fix_B"},
			{From: "analyze", To: "update_docs"},
			{From: "fix_A", To: "run_tests"},
			{From: "fix_B", To: "run_tests"},
			{From: "run_tests", To: "report"},
			{From: "update_docs", To: "report"},
		},
	}
}

// --- helpers --------------------------------------------------------------

func indexOf(order []string, id string) int {
	for i, x := range order {
		if x == id {
			return i
		}
	}
	return -1
}

func containsSubstr(problems []error, sub string) bool {
	for _, p := range problems {
		if strings.Contains(p.Error(), sub) {
			return true
		}
	}
	return false
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- TopoOrder ------------------------------------------------------------

func TestTopoOrder_Valid(t *testing.T) {
	p := bugfixPlan()
	if err := p.BasicValidate(); err != nil {
		t.Fatalf("fixture failed BasicValidate: %v", err)
	}

	order, err := TopoOrder(p)
	if err != nil {
		t.Fatalf("TopoOrder: unexpected error: %v", err)
	}
	if len(order) != len(p.Nodes) {
		t.Fatalf("TopoOrder: got %d nodes, want %d (%v)", len(order), len(p.Nodes), order)
	}

	// Every node id appears exactly once.
	seen := map[string]int{}
	for _, id := range order {
		seen[id]++
	}
	for _, n := range p.Nodes {
		if seen[n.ID] != 1 {
			t.Errorf("TopoOrder: node %q appears %d times, want 1", n.ID, seen[n.ID])
		}
	}

	// Every edge must respect the ordering: From strictly before To.
	for _, e := range p.Edges {
		if indexOf(order, e.From) >= indexOf(order, e.To) {
			t.Errorf("TopoOrder: edge %s->%s violated (positions %d, %d): %v",
				e.From, e.To, indexOf(order, e.From), indexOf(order, e.To), order)
		}
	}
}

func TestTopoOrder_Cycle(t *testing.T) {
	p := bugfixPlan()
	// Back edge report->analyze closes the loop analyze->fix_A->run_tests->report->analyze.
	p.Edges = append(p.Edges, plan.Edge{From: "report", To: "analyze"})

	order, err := TopoOrder(p)
	if err == nil {
		t.Fatalf("TopoOrder: expected a cycle error, got nil (order=%v)", order)
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("TopoOrder: error should mention a cycle, got: %v", err)
	}
	// The orderable prefix excludes the cyclic component.
	if len(order) >= len(p.Nodes) {
		t.Errorf("TopoOrder: cycle should leave some nodes unordered, got %d/%d", len(order), len(p.Nodes))
	}
}

func TestTopoOrder_Deterministic(t *testing.T) {
	p := bugfixPlan()
	first, err := TopoOrder(p)
	if err != nil {
		t.Fatalf("TopoOrder: %v", err)
	}
	for i := 0; i < 5; i++ {
		got, err := TopoOrder(p)
		if err != nil {
			t.Fatalf("TopoOrder run %d: %v", i, err)
		}
		if !equalSlices(first, got) {
			t.Fatalf("TopoOrder not deterministic:\n first=%v\n got  =%v", first, got)
		}
	}
}

// --- Check: the valid plan ------------------------------------------------

func TestCheck_Valid(t *testing.T) {
	p := bugfixPlan()
	if err := p.BasicValidate(); err != nil {
		t.Fatalf("fixture failed BasicValidate: %v", err)
	}
	if probs := Check(p); len(probs) != 0 {
		t.Fatalf("Check: expected a valid plan, got %d problem(s): %v", len(probs), probs)
	}

	// And topo-order must return all 10 nodes.
	order, err := TopoOrder(p)
	if err != nil {
		t.Fatalf("TopoOrder on valid plan: %v", err)
	}
	if len(order) != 10 {
		t.Fatalf("TopoOrder: expected 10 nodes, got %d: %v", len(order), order)
	}
}

func TestCheck_ValidWithContract(t *testing.T) {
	p := bugfixPlan()
	schema := json.RawMessage(`{"required":["patch"],"types":{"patch":"string"}}`)
	for i := range p.Nodes {
		if p.Nodes[i].ID == "fix_A" {
			p.Nodes[i].Contract = &plan.Contract{Schema: schema}
		}
	}
	if probs := Check(p); len(probs) != 0 {
		t.Fatalf("Check: a valid contract schema should pass, got: %v", probs)
	}
}

// --- Check: the five structural failures (table-driven) -------------------

func TestCheck_Failures(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*plan.Plan)
		wantSub string // substring expected in at least one reported problem
	}{
		{
			name: "check1 acyclicity: cycle detected",
			mutate: func(p *plan.Plan) {
				p.Edges = append(p.Edges, plan.Edge{From: "report", To: "analyze"})
			},
			wantSub: "acyclicity",
		},
		{
			name: "check2 reachability: unreachable + dead island",
			mutate: func(p *plan.Plan) {
				// A disconnected 2-node cycle. Neither node has an entry ancestor
				// or an exit descendant, so both are unreachable AND dead. (This
				// also trips acyclicity, which is fine - Check reports all.)
				p.Nodes = append(p.Nodes,
					plan.Node{ID: "iso1", ActionRef: "iso1", Join: plan.JoinAllOf, SideEffect: plan.SideEffectNone, RetryBudget: 1, TimeoutMS: 1000},
					plan.Node{ID: "iso2", ActionRef: "iso2", Join: plan.JoinAllOf, SideEffect: plan.SideEffectNone, RetryBudget: 1, TimeoutMS: 1000},
				)
				p.Edges = append(p.Edges,
					plan.Edge{From: "iso1", To: "iso2"},
					plan.Edge{From: "iso2", To: "iso1"},
				)
			},
			wantSub: "reachability",
		},
		{
			name: "check3 join consistency: any_of with one candidate",
			mutate: func(p *plan.Plan) {
				// run_tests is any_of; drop fix_B->run_tests so it has a single
				// predecessor. Re-home fix_B to report so it still reaches an exit
				// and the only defect is the join-consistency one.
				edges := p.Edges[:0:0]
				for _, e := range p.Edges {
					if e.From == "fix_B" && e.To == "run_tests" {
						continue
					}
					edges = append(edges, e)
				}
				edges = append(edges, plan.Edge{From: "fix_B", To: "report"})
				p.Edges = edges
			},
			wantSub: "any_of",
		},
		{
			name: "check4 contract well-formedness: invalid JSON schema",
			mutate: func(p *plan.Plan) {
				for i := range p.Nodes {
					if p.Nodes[i].ID == "analyze" {
						p.Nodes[i].Contract = &plan.Contract{Schema: json.RawMessage(`{"required": [bad`)}
					}
				}
			},
			wantSub: "contract",
		},
		{
			name: "check5 side-effect: destructive any_of candidate",
			mutate: func(p *plan.Plan) {
				// fix_A is a candidate of the any_of join run_tests.
				for i := range p.Nodes {
					if p.Nodes[i].ID == "fix_A" {
						p.Nodes[i].SideEffect = plan.SideEffectDestructive
					}
				}
			},
			wantSub: "destructive",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := bugfixPlan()
			c.mutate(p)
			// Failure fixtures stay field-level valid; the defect is structural.
			if err := p.BasicValidate(); err != nil {
				t.Fatalf("fixture should pass BasicValidate, got: %v", err)
			}
			probs := Check(p)
			if len(probs) == 0 {
				t.Fatalf("Check: expected problems for %q, got none", c.name)
			}
			if !containsSubstr(probs, c.wantSub) {
				t.Fatalf("Check: %q expected a problem containing %q, got: %v", c.name, c.wantSub, probs)
			}
		})
	}
}

// --- Check: focused single-defect cases ----------------------------------

// Minimal any_of-with-one-candidate, no other structure to confound it.
func TestCheck_AnyOfOneCandidate(t *testing.T) {
	p := &plan.Plan{
		ID: "anyof1", Version: 1,
		Nodes: []plan.Node{
			{ID: "a", ActionRef: "a", Join: plan.JoinAllOf, SideEffect: plan.SideEffectNone, RetryBudget: 1, TimeoutMS: 1000},
			{ID: "j", ActionRef: "j", Join: plan.JoinAnyOf, SideEffect: plan.SideEffectNone, RetryBudget: 1, TimeoutMS: 1000},
		},
		Edges: []plan.Edge{{From: "a", To: "j"}},
	}
	if err := p.BasicValidate(); err != nil {
		t.Fatalf("BasicValidate: %v", err)
	}
	if probs := Check(p); !containsSubstr(probs, "any_of") {
		t.Fatalf("expected an any_of join-consistency problem, got: %v", probs)
	}
}

// Minimal destructive-any_of-candidate.
func TestCheck_DestructiveAnyOfCandidate(t *testing.T) {
	p := &plan.Plan{
		ID: "dest1", Version: 1,
		Nodes: []plan.Node{
			{ID: "a", ActionRef: "a", Join: plan.JoinAllOf, SideEffect: plan.SideEffectWrite, RetryBudget: 1, TimeoutMS: 1000},
			{ID: "b", ActionRef: "b", Join: plan.JoinAllOf, SideEffect: plan.SideEffectDestructive, RetryBudget: 1, TimeoutMS: 1000},
			{ID: "j", ActionRef: "j", Join: plan.JoinAnyOf, SideEffect: plan.SideEffectNone, RetryBudget: 1, TimeoutMS: 1000},
		},
		Edges: []plan.Edge{
			{From: "a", To: "j"},
			{From: "b", To: "j"},
		},
	}
	if err := p.BasicValidate(); err != nil {
		t.Fatalf("BasicValidate: %v", err)
	}
	if probs := Check(p); !containsSubstr(probs, "destructive") {
		t.Fatalf("expected a destructive side-effect problem, got: %v", probs)
	}
}

// A destructive node that is NOT an any_of candidate (it feeds an all_of join)
// must be allowed - check 5 only fires under speculative parallel dispatch.
func TestCheck_DestructiveAllOfIsFine(t *testing.T) {
	p := bugfixPlan()
	// update_docs feeds report, which is all_of - safe to be destructive.
	for i := range p.Nodes {
		if p.Nodes[i].ID == "update_docs" {
			p.Nodes[i].SideEffect = plan.SideEffectDestructive
		}
	}
	if probs := Check(p); len(probs) != 0 {
		t.Fatalf("destructive node feeding an all_of join should pass, got: %v", probs)
	}
}

// A clean multi-exit DAG (root with two leaf children) must pass reachability.
func TestCheck_MultiExitPasses(t *testing.T) {
	p := &plan.Plan{
		ID: "diamond", Version: 1,
		Nodes: []plan.Node{
			{ID: "root", ActionRef: "root", Join: plan.JoinAllOf, SideEffect: plan.SideEffectNone, RetryBudget: 1, TimeoutMS: 1000},
			{ID: "left", ActionRef: "left", Join: plan.JoinAllOf, SideEffect: plan.SideEffectNone, RetryBudget: 1, TimeoutMS: 1000},
			{ID: "right", ActionRef: "right", Join: plan.JoinAllOf, SideEffect: plan.SideEffectNone, RetryBudget: 1, TimeoutMS: 1000},
		},
		Edges: []plan.Edge{
			{From: "root", To: "left"},
			{From: "root", To: "right"},
		},
	}
	if err := p.BasicValidate(); err != nil {
		t.Fatalf("BasicValidate: %v", err)
	}
	if probs := Check(p); len(probs) != 0 {
		t.Fatalf("root with two exit children should pass, got: %v", probs)
	}
}

// Check accumulates multiple defects in a single pass.
func TestCheck_ReportsAllProblems(t *testing.T) {
	p := bugfixPlan()
	for i := range p.Nodes {
		switch p.Nodes[i].ID {
		case "analyze":
			p.Nodes[i].Contract = &plan.Contract{Schema: json.RawMessage(`{nope`)} // defect 1
		case "fix_A":
			p.Nodes[i].SideEffect = plan.SideEffectDestructive // defect 2
		}
	}
	probs := Check(p)
	if !containsSubstr(probs, "contract") || !containsSubstr(probs, "destructive") {
		t.Fatalf("expected BOTH contract and destructive problems, got: %v", probs)
	}
	if len(probs) < 2 {
		t.Fatalf("expected >= 2 problems, got %d: %v", len(probs), probs)
	}
}

// --- internal sanity ------------------------------------------------------

// The adjacency must list every node, even isolated ones, with succ/pred keys.
func TestBuildAdjacency_AllNodesPresent(t *testing.T) {
	p := bugfixPlan()
	a := buildAdjacency(p)

	got := append([]string(nil), a.ids...)
	sort.Strings(got)
	want := make([]string, 0, len(p.Nodes))
	for _, n := range p.Nodes {
		want = append(want, n.ID)
	}
	sort.Strings(want)
	if !equalSlices(got, want) {
		t.Fatalf("adjacency ids mismatch:\n got=%v\n want=%v", got, want)
	}
	for _, n := range p.Nodes {
		if _, ok := a.succ[n.ID]; !ok {
			t.Errorf("succ missing node %q", n.ID)
		}
		if _, ok := a.pred[n.ID]; !ok {
			t.Errorf("pred missing node %q", n.ID)
		}
	}
}
