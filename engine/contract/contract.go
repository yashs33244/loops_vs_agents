package contract

import "encoding/json"

// Validate reports whether output (a JSON document) satisfies schema.
//
// v1 schema shape:
//
//	{"required":["k"...],"types":{"k":"string|number|bool|object|array"}}
//
// When ok is false, problems lists the human-readable reasons (missing keys,
// type mismatches, malformed JSON). A nil/empty schema is satisfied by any
// well-formed output.
//
// STUB: not implemented yet.
func Validate(output string, schema json.RawMessage) (ok bool, problems []string) {
	panic("not implemented: contract.Validate")
}
