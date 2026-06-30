package validate

import "sgh/engine/plan"

// Check runs the five structural well-formedness checks on p (paper Appendix
// A.2) and returns a slice of all violations found (empty/nil => the plan is
// well-formed). It assumes p has already passed plan.BasicValidate.
//
// STUB: not implemented yet.
func Check(p *plan.Plan) []error {
	panic("not implemented: validate.Check")
}

// TopoOrder returns the node ids of p in a topological order via Kahn's
// algorithm, or an error if p contains a cycle.
//
// STUB: not implemented yet.
func TopoOrder(p *plan.Plan) ([]string, error) {
	panic("not implemented: validate.TopoOrder")
}
