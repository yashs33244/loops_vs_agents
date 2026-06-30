// Package validate performs structural DAG validation of a Plan beyond the
// field-level checks in plan.BasicValidate. It implements the five well-
// formedness checks from the paper (Appendix A.2) and a topological ordering
// via Kahn's algorithm.
//
// This is the M1 milestone: acyclicity, reachability, join consistency, and
// friends - the checks the scheduler relies on before it ever dispatches a
// node.
package validate
