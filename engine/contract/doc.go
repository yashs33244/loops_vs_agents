// Package contract implements output-contract validation (paper kappa, Def 5.1
// "Contracts"). A node may only enter the `executed` state once its raw JSON
// output satisfies the node's contract; the plan-level contract gates overall
// success.
//
// v1 (this skeleton) uses a hand-rolled JSON-shape check over stdlib
// encoding/json: a required-keys list plus a per-key type check. Full JSON
// Schema validation is a v2 upgrade behind the same Validate signature
// (decision D5).
package contract
