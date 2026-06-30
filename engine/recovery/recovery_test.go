package recovery

import (
	"errors"
	"testing"

	"sgh/engine/node"
)

// transient is a transient (retryable) failure Result; structural is a
// structural (non-retryable) one. The error text is irrelevant to the policy -
// only Result.Retryable matters - but we attach one to mirror real results.
var (
	transient  = node.Result{Retryable: true, Err: errors.New("timeout")}
	structural = node.Result{Retryable: false, Err: errors.New("contract violation")}
)

// TestClassify checks Classify forwards node.Result.Retryable verbatim.
func TestClassify(t *testing.T) {
	p := NewStandardPolicy(3, 2)
	tests := []struct {
		name string
		r    node.Result
		want bool
	}{
		{"transient is retryable", transient, true},
		{"structural is not retryable", structural, false},
		{"zero-value result is not retryable", node.Result{}, false},
		{"retryable flag wins regardless of error", node.Result{Retryable: true}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.Classify(tc.r); got != tc.want {
				t.Fatalf("Classify(%+v) = %v, want %v", tc.r, got, tc.want)
			}
		})
	}
}

// TestNewStandardPolicy verifies the constructor wires budgets and clamps
// negative values to zero (so a caller can never create an unbounded policy).
func TestNewStandardPolicy(t *testing.T) {
	tests := []struct {
		name                 string
		maxRetries, maxPatch int
		wantRetries, wantPat int
	}{
		{"plain budgets", 3, 2, 3, 2},
		{"zero budgets", 0, 0, 0, 0},
		{"negative retries clamped", -1, 2, 0, 2},
		{"negative patches clamped", 3, -5, 3, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewStandardPolicy(tc.maxRetries, tc.maxPatch)
			if p.MaxRetries != tc.wantRetries || p.MaxPatches != tc.wantPat {
				t.Fatalf("NewStandardPolicy(%d,%d) = {%d,%d}, want {%d,%d}",
					tc.maxRetries, tc.maxPatch, p.MaxRetries, p.MaxPatches,
					tc.wantRetries, tc.wantPat)
			}
		})
	}
}

// TestDecideTransient drives Decide with increasing Attempt counters for a
// transient failure and asserts the strict ladder: retry up to MaxRetries,
// then patch up to MaxPatches, then replan once, then fail.
func TestDecideTransient(t *testing.T) {
	p := NewStandardPolicy(2, 2)
	tests := []struct {
		name string
		att  Attempt
		want Decision
	}{
		{
			"first transient failure -> retry (level 1)",
			Attempt{Retries: 0},
			Decision{ActionRetry, LevelLocalRetry, "transient: retry"},
		},
		{
			"second transient failure -> retry (level 1)",
			Attempt{Retries: 1},
			Decision{ActionRetry, LevelLocalRetry, "transient: retry"},
		},
		{
			"retries exhausted -> patch (level 2)",
			Attempt{Retries: 2},
			Decision{ActionRetry, LevelLocalPatch, "patch + retry"},
		},
		{
			"retries exhausted, one patch spent -> patch (level 2)",
			Attempt{Retries: 2, Patches: 1},
			Decision{ActionRetry, LevelLocalPatch, "patch + retry"},
		},
		{
			"retries + patches exhausted -> replan (level 3)",
			Attempt{Retries: 2, Patches: 2},
			Decision{ActionReplan, LevelReplan, "replan"},
		},
		{
			"everything spent and replanned -> fail",
			Attempt{Retries: 2, Patches: 2, Replanned: true},
			Decision{ActionFail, LevelReplan, "recovery exhausted"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.Decide(tc.att, transient); got != tc.want {
				t.Fatalf("Decide(%+v, transient) = %+v, want %+v", tc.att, got, tc.want)
			}
		})
	}
}

// TestDecideStructural verifies a structural (non-retryable) failure SKIPS
// level 1 entirely - even with the full retry budget available, the very first
// decision is a patch - and then follows patch -> replan -> fail.
func TestDecideStructural(t *testing.T) {
	p := NewStandardPolicy(3, 2)
	tests := []struct {
		name string
		att  Attempt
		want Decision
	}{
		{
			"structural failure skips retry -> patch (level 2)",
			Attempt{Retries: 0}, // full retry budget available, still no retry
			Decision{ActionRetry, LevelLocalPatch, "patch + retry"},
		},
		{
			"structural, one patch spent -> patch (level 2)",
			Attempt{Patches: 1},
			Decision{ActionRetry, LevelLocalPatch, "patch + retry"},
		},
		{
			"structural, patches exhausted -> replan (level 3)",
			Attempt{Patches: 2},
			Decision{ActionReplan, LevelReplan, "replan"},
		},
		{
			"structural, patches spent + replanned -> fail",
			Attempt{Patches: 2, Replanned: true},
			Decision{ActionFail, LevelReplan, "recovery exhausted"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.Decide(tc.att, structural); got != tc.want {
				t.Fatalf("Decide(%+v, structural) = %+v, want %+v", tc.att, got, tc.want)
			}
		})
	}
}

// TestBudgetsHonoredExactly checks the boundary at each budget edge: the last
// in-budget attempt stays at its level and the first over-budget attempt
// escalates, for a range of (MaxRetries, MaxPatches) settings.
func TestBudgetsHonoredExactly(t *testing.T) {
	tests := []struct {
		name                 string
		maxRetries, maxPatch int
		att                  Attempt
		result               node.Result
		wantAction           Action
		wantLevel            Level
	}{
		{"last retry in budget", 3, 2, Attempt{Retries: 2}, transient, ActionRetry, LevelLocalRetry},
		{"first retry over budget -> patch", 3, 2, Attempt{Retries: 3}, transient, ActionRetry, LevelLocalPatch},
		{"last patch in budget", 3, 2, Attempt{Retries: 3, Patches: 1}, transient, ActionRetry, LevelLocalPatch},
		{"first patch over budget -> replan", 3, 2, Attempt{Retries: 3, Patches: 2}, transient, ActionReplan, LevelReplan},
		{"zero retry budget transient -> patch immediately", 0, 1, Attempt{}, transient, ActionRetry, LevelLocalPatch},
		{"zero patch budget -> replan after retries", 1, 0, Attempt{Retries: 1}, transient, ActionReplan, LevelReplan},
		{"zero retry and zero patch -> replan immediately", 0, 0, Attempt{}, transient, ActionReplan, LevelReplan},
		{"zero everything + replanned -> fail", 0, 0, Attempt{Replanned: true}, transient, ActionFail, LevelReplan},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewStandardPolicy(tc.maxRetries, tc.maxPatch)
			got := p.Decide(tc.att, tc.result)
			if got.Action != tc.wantAction || got.Level != tc.wantLevel {
				t.Fatalf("Decide(%+v) = {%v,%v}, want {%v,%v}",
					tc.att, got.Action, got.Level, tc.wantAction, tc.wantLevel)
			}
		})
	}
}

// levelRank maps a decision to its position on the escalation ladder so we can
// assert the order is monotone non-decreasing over a recovery run. A retry at
// LevelLocalRetry is rank 1, a patch is rank 2, replan/fail are rank 3.
func levelRank(d Decision) int {
	switch {
	case d.Action == ActionRetry && d.Level == LevelLocalRetry:
		return 1
	case d.Action == ActionRetry && d.Level == LevelLocalPatch:
		return 2
	case d.Action == ActionReplan:
		return 3
	case d.Action == ActionFail:
		return 4
	default:
		return -1
	}
}

// applyDecision advances the Attempt bookkeeping exactly as the scheduler would
// after carrying out a decision, so the simulation below faithfully drives the
// policy through a full recovery run.
func applyDecision(att Attempt, d Decision) Attempt {
	switch {
	case d.Action == ActionRetry && d.Level == LevelLocalRetry:
		att.Retries++
	case d.Action == ActionRetry && d.Level == LevelLocalPatch:
		att.Patches++
	case d.Action == ActionReplan:
		att.Replanned = true
	}
	return att
}

// simulate drives the policy from a fresh Attempt, applying each decision's
// effect, until ActionFail. It returns the ordered sequence of decisions and
// guards against a non-terminating policy by capping iterations.
func simulate(t *testing.T, p *StandardPolicy, r node.Result) []Decision {
	t.Helper()
	const cap = 1000
	var seq []Decision
	att := Attempt{NodeID: "n1"}
	for i := 0; i < cap; i++ {
		d := p.Decide(att, r)
		seq = append(seq, d)
		if d.Action == ActionFail {
			return seq
		}
		att = applyDecision(att, d)
	}
	t.Fatalf("policy did not terminate within %d iterations: %+v", cap, seq)
	return nil
}

// TestEscalationOrderNeverViolated drives a complete recovery run and asserts
// the ladder rank never decreases (no skipping down) and, critically, that
// replan never precedes patch exhaustion and fail never precedes a replan -
// the two invariants of Prop 6.4.
func TestEscalationOrderNeverViolated(t *testing.T) {
	cases := []struct {
		name       string
		maxRetries int
		maxPatch   int
		result     node.Result
	}{
		{"transient full ladder", 3, 2, transient},
		{"structural full ladder", 3, 2, structural},
		{"transient tight budgets", 1, 1, transient},
		{"structural tight budgets", 1, 1, structural},
		{"zero budgets transient", 0, 0, transient},
		{"zero budgets structural", 0, 0, structural},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewStandardPolicy(tc.maxRetries, tc.maxPatch)
			seq := simulate(t, p, tc.result)

			// Rank must be monotone non-decreasing: never climb down a rung.
			prev := 0
			var sawReplan, sawFail bool
			retriesSeen, patchesSeen := 0, 0
			for i, d := range seq {
				rank := levelRank(d)
				if rank < prev {
					t.Fatalf("step %d: rank dropped from %d to %d (%+v)", i, prev, rank, d)
				}
				prev = rank

				switch {
				case d.Action == ActionRetry && d.Level == LevelLocalRetry:
					retriesSeen++
					// A structural failure must never produce a level-1 retry.
					if !tc.result.Retryable {
						t.Fatalf("step %d: structural failure produced a retry", i)
					}
				case d.Action == ActionRetry && d.Level == LevelLocalPatch:
					patchesSeen++
				case d.Action == ActionReplan:
					// Replan only after the patch budget is fully spent.
					if patchesSeen != tc.maxPatch {
						t.Fatalf("step %d: replan with %d/%d patches spent (Prop 6.4 violated)",
							i, patchesSeen, tc.maxPatch)
					}
					sawReplan = true
				case d.Action == ActionFail:
					// Fail only after a replan was attempted.
					if !sawReplan {
						t.Fatalf("step %d: fail before any replan (Prop 6.4 violated)", i)
					}
					sawFail = true
				}
			}

			if !sawFail {
				t.Fatalf("run never reached ActionFail: %+v", seq)
			}
			// Budgets honored exactly: transient spends all retries; structural
			// spends none. Both spend all patches and exactly one replan.
			wantRetries := tc.maxRetries
			if !tc.result.Retryable {
				wantRetries = 0
			}
			if retriesSeen != wantRetries {
				t.Fatalf("retries spent = %d, want %d", retriesSeen, wantRetries)
			}
			if patchesSeen != tc.maxPatch {
				t.Fatalf("patches spent = %d, want %d", patchesSeen, tc.maxPatch)
			}
		})
	}
}

// TestTerminates asserts that once budgets are spent and a replan was attempted
// the policy is at a fixed point: it returns ActionFail and keeps returning it
// (the absorbing terminal state from the FSM / Theorem 6.2).
func TestTerminates(t *testing.T) {
	p := NewStandardPolicy(5, 5)
	spent := Attempt{Retries: 5, Patches: 5, Replanned: true}

	for _, r := range []node.Result{transient, structural} {
		d := p.Decide(spent, r)
		if d.Action != ActionFail {
			t.Fatalf("spent budget did not fail: got %+v", d)
		}
		// Re-deciding from the failed state stays failed (absorbing).
		if again := p.Decide(spent, r); again.Action != ActionFail {
			t.Fatalf("ActionFail is not a fixed point: got %+v", again)
		}
	}
}

// TestTotalAttemptsBounded sanity-checks Theorem 6.2: a full recovery run takes
// at most MaxRetries + MaxPatches + 1 (replan) + 1 (fail) decisions, so it can
// never loop forever.
func TestTotalAttemptsBounded(t *testing.T) {
	p := NewStandardPolicy(4, 3)
	seq := simulate(t, p, transient)
	maxSteps := p.MaxRetries + p.MaxPatches + 2 // +1 replan, +1 fail
	if len(seq) > maxSteps {
		t.Fatalf("run took %d steps, want <= %d", len(seq), maxSteps)
	}
}
