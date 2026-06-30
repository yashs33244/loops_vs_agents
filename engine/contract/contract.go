package contract

import (
	"encoding/json"
	"fmt"
	"sort"
)

// schemaV1 is the wire shape of a v1 contract (decision D5): a list of
// required top-level keys plus a per-key JSON-type expectation.
//
//	{"required":["k1","k2"], "types":{"k1":"string","k2":"number"}}
//
// Both fields are optional: a schema may require keys without constraining
// their types, constrain types without requiring presence, or do neither.
type schemaV1 struct {
	Required []string          `json:"required"`
	Types    map[string]string `json:"types"`
}

// validTypeNames is the closed set of type tokens a v1 schema may use. These
// map onto the JSON value kinds produced by encoding/json.
var validTypeNames = map[string]bool{
	"string": true,
	"number": true,
	"bool":   true,
	"object": true,
	"array":  true,
}

// Validate reports whether output (a JSON document) satisfies schema.
//
// v1 schema shape:
//
//	{"required":["k"...],"types":{"k":"string|number|bool|object|array"}}
//
// Rules (decision D5):
//
//   - A nil, empty, or empty-object ({}) schema imposes no contract: any
//     well-formed output - indeed any input at all - is accepted (ok=true).
//   - Otherwise output must parse as a JSON object. If it does not parse, or
//     parses to a non-object (array, string, number, ...), that is a single
//     problem and validation fails.
//   - Every name in "required" must be present as a top-level key.
//   - For every (key,type) in "types" whose key is present in output, the
//     value's JSON type must match. Absent keys are not type-checked here
//     (presence is "required"'s job), so a key can be type-constrained without
//     being mandatory.
//
// ok == (len(problems) == 0). problems holds human-readable reasons, ordered
// deterministically (required-key checks first, then type checks by key) so
// callers and tests see stable output.
func Validate(output string, schema json.RawMessage) (ok bool, problems []string) {
	// No contract to enforce: nil, empty bytes, "null", or "{}" all mean
	// "anything goes". We check this before touching output so an empty
	// contract never rejects malformed output.
	if isEmptySchema(schema) {
		return true, nil
	}

	var sc schemaV1
	if err := json.Unmarshal(schema, &sc); err != nil {
		// A malformed schema is a programming/config error, not an output
		// failure. Surface it clearly rather than silently passing.
		return false, []string{fmt.Sprintf("invalid contract schema: %v", err)}
	}

	// Reject unknown type tokens up front: a typo like "strng" should be a
	// loud schema error, not a silently-skipped check.
	for _, key := range sortedKeys(sc.Types) {
		if !validTypeNames[sc.Types[key]] {
			problems = append(problems, fmt.Sprintf(
				"contract schema: key %q has unknown type %q (want one of string, number, bool, object, array)",
				key, sc.Types[key]))
		}
	}
	if len(problems) > 0 {
		return false, problems
	}

	// Parse output into a generic JSON object. We decode into a
	// json.RawMessage map so each value's type can be classified independently
	// (and so number vs bool vs string is unambiguous - unlike map[string]any,
	// which would coerce all numbers to float64 but still works; RawMessage
	// keeps us closer to the wire and lets us report the actual token).
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &obj); err != nil {
		return false, []string{fmt.Sprintf("output is not a valid JSON object: %v", err)}
	}

	// Required-key checks first (stable order: as listed in the schema).
	for _, key := range sc.Required {
		if _, present := obj[key]; !present {
			problems = append(problems, fmt.Sprintf("missing required key %q", key))
		}
	}

	// Type checks next, ordered by key for deterministic output.
	for _, key := range sortedKeys(sc.Types) {
		want := sc.Types[key]
		raw, present := obj[key]
		if !present {
			// Presence is enforced by "required" only; an absent key here is
			// not a type mismatch.
			continue
		}
		got := jsonType(raw)
		if got != want {
			problems = append(problems, fmt.Sprintf(
				"key %q has type %s, want %s", key, got, want))
		}
	}

	return len(problems) == 0, problems
}

// isEmptySchema reports whether schema carries no constraints: nil/empty bytes,
// the JSON null literal, or an empty object. Whitespace-only and "{}" with
// inner spaces are handled by trimming.
func isEmptySchema(schema json.RawMessage) bool {
	if len(schema) == 0 {
		return true
	}
	// Trim ASCII JSON whitespace without pulling in strings/unicode for one use.
	s := schema
	for len(s) > 0 {
		switch s[0] {
		case ' ', '\t', '\n', '\r':
			s = s[1:]
		default:
			goto done
		}
	}
done:
	str := string(s)
	return str == "" || str == "null" || str == "{}"
}

// jsonType classifies a single JSON value (as raw bytes) into one of the v1
// type tokens, or a descriptive token ("null", "unknown") for values that
// match none of them. It relies on json.Unmarshal having already validated
// that raw is well-formed JSON.
func jsonType(raw json.RawMessage) string {
	// Skip leading whitespace to find the first significant byte.
	i := 0
	for i < len(raw) {
		switch raw[i] {
		case ' ', '\t', '\n', '\r':
			i++
			continue
		}
		break
	}
	if i >= len(raw) {
		return "unknown"
	}
	switch raw[i] {
	case '"':
		return "string"
	case '{':
		return "object"
	case '[':
		return "array"
	case 't', 'f':
		return "bool"
	case 'n':
		return "null"
	default:
		// '-' or a digit begins a JSON number.
		if raw[i] == '-' || (raw[i] >= '0' && raw[i] <= '9') {
			return "number"
		}
		return "unknown"
	}
}

// sortedKeys returns the map's keys in lexical order so iteration (and thus the
// problems slice) is deterministic.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
