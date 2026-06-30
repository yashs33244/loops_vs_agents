package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestValidate is the primary table-driven exercise of the v1 contract check
// (decision D5). Each case names the scenario, supplies an output document and
// a schema, and asserts the ok verdict plus any substrings that must appear in
// the human-readable problems. wantProblems lets us pin the count without
// over-fitting on exact wording; wantSubstr pins the load-bearing text.
func TestValidate(t *testing.T) {
	cases := []struct {
		name         string
		output       string
		schema       string
		wantOK       bool
		wantProblems int      // expected len(problems); -1 means "don't check count"
		wantSubstr   []string // each must appear somewhere in the joined problems
	}{
		{
			name:   "satisfied contract",
			output: `{"k1":"hello","k2":42}`,
			schema: `{"required":["k1","k2"],"types":{"k1":"string","k2":"number"}}`,
			wantOK: true,
		},
		{
			name:   "satisfied with extra keys allowed",
			output: `{"k1":"hello","k2":42,"extra":true}`,
			schema: `{"required":["k1"],"types":{"k1":"string"}}`,
			wantOK: true,
		},
		{
			name:         "missing required key",
			output:       `{"k1":"hello"}`,
			schema:       `{"required":["k1","k2"],"types":{"k1":"string","k2":"number"}}`,
			wantOK:       false,
			wantProblems: 1,
			wantSubstr:   []string{`missing required key "k2"`},
		},
		{
			name:         "multiple missing required keys",
			output:       `{}`,
			schema:       `{"required":["k1","k2"]}`,
			wantOK:       false,
			wantProblems: 2,
			wantSubstr:   []string{`missing required key "k1"`, `missing required key "k2"`},
		},
		{
			name:         "wrong type: number where string expected",
			output:       `{"k1":42,"k2":42}`,
			schema:       `{"required":["k1","k2"],"types":{"k1":"string","k2":"number"}}`,
			wantOK:       false,
			wantProblems: 1,
			wantSubstr:   []string{`key "k1" has type number, want string`},
		},
		{
			name:         "wrong type: string where number expected",
			output:       `{"k2":"not-a-number"}`,
			schema:       `{"types":{"k2":"number"}}`,
			wantOK:       false,
			wantProblems: 1,
			wantSubstr:   []string{`key "k2" has type string, want number`},
		},
		{
			name:         "output not valid JSON",
			output:       `not json at all`,
			schema:       `{"required":["k1"]}`,
			wantOK:       false,
			wantProblems: 1,
			wantSubstr:   []string{"not a valid JSON object"},
		},
		{
			name:         "output is valid JSON but not an object",
			output:       `[1,2,3]`,
			schema:       `{"required":["k1"]}`,
			wantOK:       false,
			wantProblems: 1,
			wantSubstr:   []string{"not a valid JSON object"},
		},
		{
			name:         "output is a bare JSON string, not an object",
			output:       `"just a string"`,
			schema:       `{"required":["k1"]}`,
			wantOK:       false,
			wantProblems: 1,
			wantSubstr:   []string{"not a valid JSON object"},
		},
		{
			name:   "empty schema bytes accept anything",
			output: `{"anything":"goes"}`,
			schema: ``,
			wantOK: true,
		},
		{
			name:   "empty object schema accepts anything",
			output: `{"anything":"goes"}`,
			schema: `{}`,
			wantOK: true,
		},
		{
			name:   "null schema accepts anything",
			output: `{"anything":"goes"}`,
			schema: `null`,
			wantOK: true,
		},
		{
			name:   "whitespace-padded empty object schema accepts anything",
			output: `{"anything":"goes"}`,
			schema: "  {}  ",
			wantOK: true,
		},
		{
			name:   "empty schema does not even require valid JSON output",
			output: `this is not json`,
			schema: ``,
			wantOK: true,
		},
		{
			name:   "nested object type matches",
			output: `{"meta":{"a":1,"b":2}}`,
			schema: `{"required":["meta"],"types":{"meta":"object"}}`,
			wantOK: true,
		},
		{
			name:         "nested object type mismatch (array given)",
			output:       `{"meta":[1,2,3]}`,
			schema:       `{"required":["meta"],"types":{"meta":"object"}}`,
			wantOK:       false,
			wantProblems: 1,
			wantSubstr:   []string{`key "meta" has type array, want object`},
		},
		{
			name:   "array type matches",
			output: `{"items":[1,2,3]}`,
			schema: `{"required":["items"],"types":{"items":"array"}}`,
			wantOK: true,
		},
		{
			name:   "empty array still classified as array",
			output: `{"items":[]}`,
			schema: `{"types":{"items":"array"}}`,
			wantOK: true,
		},
		{
			name:   "empty object still classified as object",
			output: `{"meta":{}}`,
			schema: `{"types":{"meta":"object"}}`,
			wantOK: true,
		},
		{
			name:         "array type mismatch (object given)",
			output:       `{"items":{"a":1}}`,
			schema:       `{"types":{"items":"array"}}`,
			wantOK:       false,
			wantProblems: 1,
			wantSubstr:   []string{`key "items" has type object, want array`},
		},
		{
			name:   "bool type matches true",
			output: `{"flag":true}`,
			schema: `{"required":["flag"],"types":{"flag":"bool"}}`,
			wantOK: true,
		},
		{
			name:   "bool type matches false",
			output: `{"flag":false}`,
			schema: `{"types":{"flag":"bool"}}`,
			wantOK: true,
		},
		{
			name:         "bool type mismatch (string given)",
			output:       `{"flag":"true"}`,
			schema:       `{"types":{"flag":"bool"}}`,
			wantOK:       false,
			wantProblems: 1,
			wantSubstr:   []string{`key "flag" has type string, want bool`},
		},
		{
			name:   "negative and float numbers classified as number",
			output: `{"a":-5,"b":3.14,"c":1e10}`,
			schema: `{"types":{"a":"number","b":"number","c":"number"}}`,
			wantOK: true,
		},
		{
			name:   "type-constrained key absent is not a type error",
			output: `{"present":"x"}`,
			schema: `{"types":{"absent":"number","present":"string"}}`,
			wantOK: true,
		},
		{
			name:         "both missing-required and wrong-type reported together",
			output:       `{"k1":99}`,
			schema:       `{"required":["k1","k2"],"types":{"k1":"string","k2":"number"}}`,
			wantOK:       false,
			wantProblems: 2,
			wantSubstr: []string{
				`missing required key "k2"`,
				`key "k1" has type number, want string`,
			},
		},
		{
			name:         "unknown type token in schema is a schema error",
			output:       `{"k1":"x"}`,
			schema:       `{"types":{"k1":"strng"}}`,
			wantOK:       false,
			wantProblems: 1,
			wantSubstr:   []string{"unknown type", `"strng"`},
		},
		{
			name:         "malformed schema bytes surface a schema error",
			output:       `{"k1":"x"}`,
			schema:       `{"required": not-json}`,
			wantOK:       false,
			wantProblems: 1,
			wantSubstr:   []string{"invalid contract schema"},
		},
		{
			name:   "null value does not satisfy string type",
			output: `{"k1":null}`,
			schema: `{"types":{"k1":"string"}}`,
			wantOK: false,
			// jsonType reports "null"; the message names the actual type.
			wantProblems: 1,
			wantSubstr:   []string{`key "k1" has type null, want string`},
		},
		{
			name:   "null value is still a present key for required",
			output: `{"k1":null}`,
			schema: `{"required":["k1"]}`,
			wantOK: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, problems := Validate(c.output, json.RawMessage(c.schema))
			if ok != c.wantOK {
				t.Errorf("ok = %v, want %v (problems: %v)", ok, c.wantOK, problems)
			}
			// Invariant: ok must always equal (len(problems)==0).
			if ok != (len(problems) == 0) {
				t.Errorf("invariant violated: ok=%v but len(problems)=%d (%v)", ok, len(problems), problems)
			}
			if c.wantProblems >= 0 && len(problems) != c.wantProblems {
				t.Errorf("len(problems) = %d, want %d: %v", len(problems), c.wantProblems, problems)
			}
			joined := strings.Join(problems, " | ")
			for _, sub := range c.wantSubstr {
				if !strings.Contains(joined, sub) {
					t.Errorf("problems missing expected substring %q; got: %v", sub, problems)
				}
			}
		})
	}
}

// TestValidate_DeterministicOrder pins that repeated calls on the same inputs
// yield byte-identical problem slices. Map iteration in Go is randomized, so
// without the sort in Validate this would flake; this test guards that.
func TestValidate_DeterministicOrder(t *testing.T) {
	output := `{"z":1,"a":1,"m":1}` // all numbers, all wrong vs string
	schema := json.RawMessage(`{"types":{"z":"string","a":"string","m":"string"}}`)

	_, first := Validate(output, schema)
	if len(first) != 3 {
		t.Fatalf("expected 3 problems, got %d: %v", len(first), first)
	}
	for i := 0; i < 50; i++ {
		_, again := Validate(output, schema)
		if len(again) != len(first) {
			t.Fatalf("length drift on iter %d: %v vs %v", i, again, first)
		}
		for j := range first {
			if again[j] != first[j] {
				t.Fatalf("order drift on iter %d at index %d: %q vs %q", i, j, again[j], first[j])
			}
		}
	}
}

// TestValidate_OKImpliesNilLikeProblems documents the contract that a passing
// validation returns no problems.
func TestValidate_OKImpliesNoProblems(t *testing.T) {
	ok, problems := Validate(`{"k1":"v"}`, json.RawMessage(`{"required":["k1"],"types":{"k1":"string"}}`))
	if !ok {
		t.Fatalf("expected ok, got problems: %v", problems)
	}
	if len(problems) != 0 {
		t.Fatalf("expected no problems on success, got: %v", problems)
	}
}
