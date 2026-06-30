// Package plan defines the immutable data model for a Graph Harness execution
// plan: the typed Plan/Node/Edge structures, the node lifecycle states, and the
// per-node output contracts (paper Def 5.1, 6.1).
//
// This is the M0 milestone: data model + (de)serialization + field-level
// well-formedness only. The full DAG checks (acyclicity, reachability, join
// consistency - paper Appendix A.2) live in the validator package (M1), and
// scheduling lives in the scheduler package (M2+).
//
// A Plan is immutable: a replan mints a new (id, version+1); a Plan value is
// never mutated in place (paper Def 5.2, the plan invariant).
package plan

import (
	"encoding/json"
	"fmt"
)

// JoinMode determines when a node becomes ready relative to its predecessors.
//
//	all_of: ready once EVERY predecessor reaches a terminal-success state.
//	any_of: ready once ANY candidate reaches executed; remaining siblings skipped.
//
// (paper Def 7.1, 7.2. first_of / competitive parallelism is deliberately excluded.)
type JoinMode string

const (
	JoinAllOf JoinMode = "all_of"
	JoinAnyOf JoinMode = "any_of"
)

func (j JoinMode) Valid() bool { return j == JoinAllOf || j == JoinAnyOf }

// SideEffectLevel classifies how dangerous a node is (paper Principle 4). The
// scheduler must never speculatively parallel-dispatch a destructive node.
type SideEffectLevel string

const (
	SideEffectNone        SideEffectLevel = "none"
	SideEffectRead        SideEffectLevel = "read"
	SideEffectWrite       SideEffectLevel = "write"
	SideEffectDestructive SideEffectLevel = "destructive"
)

func (s SideEffectLevel) Valid() bool {
	switch s {
	case SideEffectNone, SideEffectRead, SideEffectWrite, SideEffectDestructive:
		return true
	}
	return false
}

// NodeState is the per-node lifecycle state (paper Def 6.1, Table 15).
//
//	pending -> ready -> running -> {executed | failed_retryable | failed | waiting_human | blocked}
//	failed_retryable -> {pending (retry) | failed (budget exhausted) | skipped (any_of sibling won)}
//
// The full transition table is enforced by the FSM package (M2).
type NodeState string

const (
	StatePending      NodeState = "pending"
	StateReady        NodeState = "ready"
	StateRunning      NodeState = "running"
	StateWaitingHuman NodeState = "waiting_human"
	StateBlocked      NodeState = "blocked"
	StateExecuted     NodeState = "executed"
	StateFailedRetry  NodeState = "failed_retryable"
	StateFailed       NodeState = "failed"
	StateCancelled    NodeState = "cancelled"
	StateSkipped      NodeState = "skipped"
)

// IsTerminal reports whether the state is absorbing (paper: executed, failed,
// cancelled, skipped). Once terminal, a node never transitions again - this is
// what lets the scheduler trust a predecessor's completion permanently.
func (s NodeState) IsTerminal() bool {
	switch s {
	case StateExecuted, StateFailed, StateCancelled, StateSkipped:
		return true
	}
	return false
}

// IsTerminalSuccess reports a terminal state that satisfies a dependency.
// all_of joins require executed; any_of joins accept executed or skipped
// (paper Appendix A.1: Sigma_term^+).
func (s NodeState) IsTerminalSuccess() bool {
	return s == StateExecuted || s == StateSkipped
}

func (s NodeState) Valid() bool {
	switch s {
	case StatePending, StateReady, StateRunning, StateWaitingHuman, StateBlocked,
		StateExecuted, StateFailedRetry, StateFailed, StateCancelled, StateSkipped:
		return true
	}
	return false
}

// Contract is an output contract (paper kappa): a JSON Schema the output must
// satisfy before a node may enter `executed`. Stored raw at M0; schema
// compilation + validation is wired in a later milestone.
type Contract struct {
	Schema json.RawMessage `json:"schema,omitempty"`
}

// Node is a single executable unit and its configuration (paper sigma: V -> NodeConfig).
type Node struct {
	ID          string          `json:"id"`
	ActionRef   string          `json:"action_ref"` // selects which executor runs this node
	Join        JoinMode        `json:"join"`
	SideEffect  SideEffectLevel `json:"side_effect"`
	RetryBudget int             `json:"retry_budget"` // max local_retry attempts (paper b_v)
	TimeoutMS   int64           `json:"timeout_ms"`   // per-node timeout (paper tau_v)
	Contract    *Contract       `json:"contract,omitempty"`
}

// Edge is a directed dependency: From must reach terminal-success before To
// can become ready.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Plan is the immutable execution plan (paper Def 5.1: id, version, V, E, sigma, kappa).
type Plan struct {
	ID       string    `json:"id"`
	Version  int       `json:"version"`
	Nodes    []Node    `json:"nodes"`
	Edges    []Edge    `json:"edges"`
	Contract *Contract `json:"contract,omitempty"` // plan-level output contract (kappa)
}

// FromJSON parses a Plan from its JSON representation (the boundary shared with
// the Python eval harness).
func FromJSON(b []byte) (*Plan, error) {
	var p Plan
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("plan: unmarshal: %w", err)
	}
	return &p, nil
}

// ToJSON renders the Plan as indented JSON.
func (p *Plan) ToJSON() ([]byte, error) {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("plan %s: marshal: %w", p.ID, err)
	}
	return b, nil
}

// BasicValidate checks field-level well-formedness ONLY: ids present and unique,
// enums valid, edges reference existing nodes, no self-loops. The structural DAG
// checks (acyclicity, reachability, join consistency) are the validator package
// (M1) - this method deliberately does not do them.
func (p *Plan) BasicValidate() error {
	if p.ID == "" {
		return fmt.Errorf("plan: empty id")
	}
	if p.Version < 1 {
		return fmt.Errorf("plan %s: version must be >= 1, got %d", p.ID, p.Version)
	}
	if len(p.Nodes) == 0 {
		return fmt.Errorf("plan %s: no nodes", p.ID)
	}
	seen := make(map[string]bool, len(p.Nodes))
	for _, n := range p.Nodes {
		if n.ID == "" {
			return fmt.Errorf("plan %s: node with empty id", p.ID)
		}
		if seen[n.ID] {
			return fmt.Errorf("plan %s: duplicate node id %q", p.ID, n.ID)
		}
		seen[n.ID] = true
		if !n.Join.Valid() {
			return fmt.Errorf("node %s: invalid join mode %q", n.ID, n.Join)
		}
		if !n.SideEffect.Valid() {
			return fmt.Errorf("node %s: invalid side-effect level %q", n.ID, n.SideEffect)
		}
		if n.RetryBudget < 0 {
			return fmt.Errorf("node %s: negative retry budget %d", n.ID, n.RetryBudget)
		}
		if n.TimeoutMS < 0 {
			return fmt.Errorf("node %s: negative timeout %d", n.ID, n.TimeoutMS)
		}
	}
	for i, e := range p.Edges {
		if !seen[e.From] {
			return fmt.Errorf("edge %d: from-node %q does not exist", i, e.From)
		}
		if !seen[e.To] {
			return fmt.Errorf("edge %d: to-node %q does not exist", i, e.To)
		}
		if e.From == e.To {
			return fmt.Errorf("edge %d: self-loop on %q", i, e.From)
		}
	}
	return nil
}
