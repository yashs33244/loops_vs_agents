package validate

import (
	"encoding/json"
	"fmt"
	"sort"

	"sgh/engine/plan"
)

// adjacency holds the predecessor/successor maps derived from p.Edges, plus the
// node lookup and the stable node-id order. Every node id is present as a key in
// both succ and pred (with an empty slice if it has none), so callers can range
// over the maps without missing isolated nodes.
type adjacency struct {
	ids   []string                  // node ids in plan order (stable iteration)
	nodes map[string]plan.Node      // id -> node config
	succ  map[string][]string       // id -> successor ids
	pred  map[string][]string       // id -> predecessor ids
}

// buildAdjacency derives predecessor/successor adjacency lists from p.Edges. It
// assumes p has already passed plan.BasicValidate (ids unique, edges reference
// existing nodes, no self-loops), so it does not re-validate those properties.
func buildAdjacency(p *plan.Plan) *adjacency {
	a := &adjacency{
		ids:   make([]string, 0, len(p.Nodes)),
		nodes: make(map[string]plan.Node, len(p.Nodes)),
		succ:  make(map[string][]string, len(p.Nodes)),
		pred:  make(map[string][]string, len(p.Nodes)),
	}
	for _, n := range p.Nodes {
		a.ids = append(a.ids, n.ID)
		a.nodes[n.ID] = n
		// Ensure every node has an entry even with no edges.
		if _, ok := a.succ[n.ID]; !ok {
			a.succ[n.ID] = nil
		}
		if _, ok := a.pred[n.ID]; !ok {
			a.pred[n.ID] = nil
		}
	}
	for _, e := range p.Edges {
		a.succ[e.From] = append(a.succ[e.From], e.To)
		a.pred[e.To] = append(a.pred[e.To], e.From)
	}
	return a
}

// TopoOrder returns the node ids of p in a topological order via Kahn's
// algorithm, or an error if p contains a cycle (paper Appendix A.2, check 1:
// (V,E) must be a DAG). Ties are broken by node-plan order so the result is
// deterministic; the scheduler depends on a stable ordering.
//
// On a cycle the returned slice holds the prefix that *was* orderable (the nodes
// outside the cycle) and the error names how many nodes could not be ordered.
func TopoOrder(p *plan.Plan) ([]string, error) {
	a := buildAdjacency(p)

	// indegree[v] = number of predecessors of v not yet emitted.
	indeg := make(map[string]int, len(a.ids))
	for _, id := range a.ids {
		indeg[id] = len(a.pred[id])
	}

	// Seed the queue with every zero-indegree node, in plan order.
	var queue []string
	for _, id := range a.ids {
		if indeg[id] == 0 {
			queue = append(queue, id)
		}
	}

	order := make([]string, 0, len(a.ids))
	for len(queue) > 0 {
		// Pop the smallest-by-plan-order ready node for determinism. The queue
		// stays sorted by plan index on each step.
		sortByPlanOrder(queue, a)
		id := queue[0]
		queue = queue[1:]
		order = append(order, id)

		for _, s := range a.succ[id] {
			indeg[s]--
			if indeg[s] == 0 {
				queue = append(queue, s)
			}
		}
	}

	if len(order) != len(a.ids) {
		return order, fmt.Errorf("validate: cycle detected: %d of %d nodes could not be ordered",
			len(a.ids)-len(order), len(a.ids))
	}
	return order, nil
}

// sortByPlanOrder sorts ids in place by their index in the plan's node list.
func sortByPlanOrder(ids []string, a *adjacency) {
	idx := func(id string) int {
		for i, n := range a.ids {
			if n == id {
				return i
			}
		}
		return len(a.ids)
	}
	sort.SliceStable(ids, func(i, j int) bool { return idx(ids[i]) < idx(ids[j]) })
}

// Check runs the five structural well-formedness checks on p (paper Appendix
// A.2) and returns a slice of all violations found (empty/nil => the plan is
// well-formed). It assumes p has already passed plan.BasicValidate.
//
// The five checks:
//  1. Acyclicity: (V,E) must be a DAG. If Kahn's sort can't order all nodes,
//     report a cycle.
//  2. Reachability: every node must be reachable from some entry node (a node
//     with no predecessors) AND able to reach some exit node (no successors).
//     Report unreachable / dead nodes.
//  3. Join consistency: a node with any_of must have >= 2 predecessors
//     (candidates); a non-entry node with all_of must have >= 1 predecessor.
//  4. Contract well-formedness: a node whose Contract has a non-empty Schema
//     must carry valid JSON in that schema.
//  5. Side-effect consistency: a destructive node must NOT be a candidate of an
//     any_of join (it would be speculatively run in parallel).
//
// Check returns ALL problems found rather than stopping at the first, so a plan
// author sees every issue in one pass.
func Check(p *plan.Plan) []error {
	var problems []error
	a := buildAdjacency(p)

	// Check 1: Acyclicity (Kahn's algorithm). If the topological sort cannot
	// order every node, the graph has a cycle.
	_, topoErr := TopoOrder(p)
	if topoErr != nil {
		problems = append(problems, fmt.Errorf("acyclicity: %w", topoErr))
	}

	// Check 2: Reachability. Computed over the raw graph so it works with or
	// without a cycle. In a connected DAG every node trivially has a
	// predecessor-less ancestor (entry) and a successor-less descendant (exit);
	// the check earns its keep on disconnected components and cyclic islands,
	// whose nodes reach no entry forward and no exit forward and are flagged as
	// unreachable / dead.
	problems = append(problems, reachabilityProblems(a)...)

	// Identify any_of candidates once for checks 3 and 5. A node v is an "any_of
	// candidate" if at least one of its successors has join mode any_of, i.e. v is
	// one of the alternatives that successor races. (In this model the join mode
	// lives on the joining node and applies to all its predecessors.)
	anyOfCandidate := make(map[string]bool, len(a.ids))
	for _, id := range a.ids {
		if a.nodes[id].Join == plan.JoinAnyOf {
			for _, pr := range a.pred[id] {
				anyOfCandidate[pr] = true
			}
		}
	}

	// Per-node checks 3, 4, 5 in stable plan order.
	for _, id := range a.ids {
		n := a.nodes[id]
		preds := a.pred[id]

		// Check 3: Join consistency.
		switch n.Join {
		case plan.JoinAnyOf:
			if len(preds) < 2 {
				problems = append(problems, fmt.Errorf(
					"join consistency: node %q uses any_of but has %d predecessor(s) (need >= 2 candidates)",
					id, len(preds)))
			}
		case plan.JoinAllOf:
			// A non-entry all_of node must have at least one predecessor.
			// Entry nodes (no predecessors) are fine: they start the graph.
			// (len(preds)==0 is an entry node; nothing to enforce.)
		}

		// Check 4: Contract well-formedness.
		if n.Contract != nil && len(n.Contract.Schema) > 0 {
			if !json.Valid(n.Contract.Schema) {
				problems = append(problems, fmt.Errorf(
					"contract well-formedness: node %q has a non-empty contract schema that is not valid JSON", id))
			}
		}

		// Check 5: Side-effect consistency.
		if n.SideEffect == plan.SideEffectDestructive && anyOfCandidate[id] {
			problems = append(problems, fmt.Errorf(
				"side-effect consistency: destructive node %q is a candidate of an any_of join "+
					"(it would be speculatively run in parallel)", id))
		}
	}

	return problems
}

// reachabilityProblems implements check 2. Entry nodes are those with no
// predecessors; exit nodes are those with no successors. Every node must be
// reachable forward from some entry node AND able to reach some exit node going
// forward. Problems are reported in stable plan order. The traversals are over
// the raw graph (no acyclicity assumption): a node trapped in a disconnected
// cycle has no entry ancestor and no exit descendant, so it is flagged as both
// unreachable and dead.
func reachabilityProblems(a *adjacency) []error {
	var problems []error

	// Forward reachability from entry nodes: which nodes can be reached by
	// following edges from any predecessor-less node.
	reachableFromEntry := make(map[string]bool, len(a.ids))
	var stack []string
	for _, id := range a.ids {
		if len(a.pred[id]) == 0 {
			if !reachableFromEntry[id] {
				reachableFromEntry[id] = true
				stack = append(stack, id)
			}
		}
	}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, s := range a.succ[id] {
			if !reachableFromEntry[s] {
				reachableFromEntry[s] = true
				stack = append(stack, s)
			}
		}
	}

	// Backward reachability to exit nodes: which nodes can reach some
	// successor-less node by following edges forward. Computed by a reverse
	// traversal seeded at the exit nodes.
	canReachExit := make(map[string]bool, len(a.ids))
	stack = stack[:0]
	for _, id := range a.ids {
		if len(a.succ[id]) == 0 {
			if !canReachExit[id] {
				canReachExit[id] = true
				stack = append(stack, id)
			}
		}
	}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, pr := range a.pred[id] {
			if !canReachExit[pr] {
				canReachExit[pr] = true
				stack = append(stack, pr)
			}
		}
	}

	for _, id := range a.ids {
		if !reachableFromEntry[id] {
			problems = append(problems, fmt.Errorf(
				"reachability: node %q is unreachable from any entry node", id))
		}
		if !canReachExit[id] {
			problems = append(problems, fmt.Errorf(
				"reachability: node %q is a dead node (cannot reach any exit node)", id))
		}
	}
	return problems
}
