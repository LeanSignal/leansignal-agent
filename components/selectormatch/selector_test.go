// Copyright 2026 LeanSignal
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package leansignalselectormatch

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Parse (Prometheus label names) — valid inputs
// (ported unchanged from components/logdemandfilter/selector_test.go)
// ---------------------------------------------------------------------------

func TestParseSelectorValid(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		matchers int
	}{
		{"single equality", `{service_name="api"}`, 1},
		{"two matchers", `{service_name="api",k8s_namespace_name="prod"}`, 2},
		{"spaces around tokens", `{ service_name = "api" , cloud_region = "eu" }`, 2},
		{"all four ops", `{a="1",b!="2",c=~"x.*",d!~"y+"}`, 4},
		{"trailing comma tolerated", `{a="1",}`, 1},
		{"escaped quote in value", `{a="say \"hi\""}`, 1},
		{"escaped backslash", `{a="c:\\temp"}`, 1},
		{"unicode escape", `{a="é"}`, 1},
		{"backquoted raw value", "{a=`raw\\value`}", 1},
		{"empty value", `{a=""}`, 1},
		{"underscore-heavy name", `{__name__="x"}`, 1},
		{"surrounding whitespace", `  {a="1"}  `, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sel, err := Parse(c.input)
			if err != nil {
				t.Fatalf("Parse(%q) failed: %v", c.input, err)
			}
			if len(sel.Matchers) != c.matchers {
				t.Errorf("matchers: got %d want %d", len(sel.Matchers), c.matchers)
			}
		})
	}
}

func TestParseSelectorUnquotesValues(t *testing.T) {
	sel, err := Parse(`{a="say \"hi\"", b="tab\there"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := sel.Matchers[0].Value; got != `say "hi"` {
		t.Errorf("escaped quotes: got %q", got)
	}
	if got := sel.Matchers[1].Value; got != "tab\there" {
		t.Errorf("escaped tab: got %q", got)
	}

	sel, err = Parse("{a=`no\\escape`}")
	if err != nil {
		t.Fatal(err)
	}
	if got := sel.Matchers[0].Value; got != `no\escape` {
		t.Errorf("raw string: got %q (backslash must stay literal)", got)
	}
}

func TestParseSelectorValueWithBraceAndComma(t *testing.T) {
	// '}' and ',' inside a quoted value must not terminate parsing.
	sel, err := Parse(`{a="x}y,z"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := sel.Matchers[0].Value; got != "x}y,z" {
		t.Errorf("value: got %q want %q", got, "x}y,z")
	}
}

// ---------------------------------------------------------------------------
// Parse — invalid inputs
// ---------------------------------------------------------------------------

func TestParseSelectorInvalid(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"empty selector", "{}"},
		{"empty selector with spaces", "{  }"},
		{"missing opening brace", `a="1"}`},
		{"missing closing brace", `{a="1"`},
		{"bare metric name", `up`},
		{"unquoted value", `{a=api}`},
		{"single-quoted value", `{a='api'}`},
		{"unterminated quote", `{a="api}`},
		{"unterminated raw", "{a=`api}"},
		{"bad operator", `{a=="1"}`},
		{"missing operator", `{a "1"}`},
		{"missing value", `{a=}`},
		{"label starting with digit", `{1a="x"}`},
		{"label with dot", `{service.name="x"}`},
		{"invalid regex", `{a=~"["}`},
		{"invalid negated regex", `{a!~"(unclosed"}`},
		{"trailing garbage", `{a="1"} |= "err"`},
		{"two selectors", `{a="1"}{b="2"}`},
		{"lonely comma", `{,}`},
		{"bad escape", `{a="\q"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if sel, err := Parse(c.input); err == nil {
				t.Errorf("Parse(%q) = %+v, want error", c.input, sel)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Matching semantics (Prometheus rules)
// ---------------------------------------------------------------------------

func mustParse(t *testing.T, s string) *Selector {
	t.Helper()
	sel, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return sel
}

func TestMatcherSemantics(t *testing.T) {
	labels := map[string]string{
		"service_name":       "api",
		"k8s_namespace_name": "prod",
	}
	cases := []struct {
		name     string
		selector string
		want     bool
	}{
		{"exact match", `{service_name="api"}`, true},
		{"exact mismatch", `{service_name="web"}`, false},
		{"all matchers must match", `{service_name="api",k8s_namespace_name="dev"}`, false},
		{"both match", `{service_name="api",k8s_namespace_name="prod"}`, true},

		// = with empty value matches an ABSENT label.
		{"empty value matches absent label", `{container_name=""}`, true},
		{"empty value does not match present label", `{service_name=""}`, false},

		// != negation; an absent label ("") is != any non-empty value.
		{"negation mismatch passes", `{service_name!="web"}`, true},
		{"negation match fails", `{service_name!="api"}`, false},
		{"negation on absent label passes", `{container_name!="x"}`, true},
		{"negation empty on present label passes", `{service_name!=""}`, true},
		{"negation empty on absent label fails", `{container_name!=""}`, false},

		// =~ fully anchored RE2.
		{"regex match", `{service_name=~"a.*"}`, true},
		{"regex is anchored (prefix only)", `{service_name=~"a"}`, false},
		{"regex is anchored (substring)", `{service_name=~"p"}`, false},
		{"regex full alternation", `{service_name=~"api|web"}`, true},
		{"regex anchored alternation groups", `{service_name=~"ap(i|e)"}`, true},

		// Empty-matching regex matches an absent label.
		{"empty-matching regex matches absent", `{container_name=~".*"}`, true},
		{"empty-matching optional matches absent", `{container_name=~"(x)?"}`, true},
		{"non-empty regex does not match absent", `{container_name=~".+"}`, false},

		// !~ negated regex.
		{"negated regex passes on mismatch", `{service_name!~"web.*"}`, true},
		{"negated regex fails on match", `{service_name!~"api"}`, false},
		{"negated empty-matching regex fails on absent", `{container_name!~".*"}`, false},
		{"negated non-empty regex passes on absent", `{container_name!~".+"}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mustParse(t, c.selector).Matches(labels); got != c.want {
				t.Errorf("%s on %v = %v, want %v", c.selector, labels, got, c.want)
			}
		})
	}
}

func TestMatcherRegexAnchoringWithMetaChars(t *testing.T) {
	// "^(?:v)$" wrapping must not let a top-level alternation escape the anchor.
	sel := mustParse(t, `{a=~"x|y"}`)
	if sel.Matches(map[string]string{"a": "xz", "service_name": "s"}) {
		t.Error(`{a=~"x|y"} must not match "xz" (alternation must stay anchored)`)
	}
	if !sel.Matches(map[string]string{"a": "y", "service_name": "s"}) {
		t.Error(`{a=~"x|y"} must match "y"`)
	}
}

// ---------------------------------------------------------------------------
// ParseDotted (dotted attribute-key names)
// (ported unchanged from components/tracedemandfilter/selector_test.go)
// ---------------------------------------------------------------------------

func TestParseDottedValid(t *testing.T) {
	cases := []struct {
		in       string
		matchers int
	}{
		{`{resource.service.name="checkout"}`, 1},
		{`{resource.service.name="checkout",resource.deployment.environment="prod"}`, 2},
		{`{ resource.service.name = "a" , resource.k8s.namespace.name != "kube-system" }`, 2},
		{`{resource.service.name=~"check.*"}`, 1},
		{`{resource.service.name!~"internal-.*"}`, 1},
		{`{resource.service.name="a",}`, 1}, // trailing comma tolerated
		{"{resource.service.name=`raw`}", 1},
		{`{plain_underscore_name="x"}`, 1}, // undotted names still valid
	}
	for _, c := range cases {
		sel, err := ParseDotted(c.in)
		if err != nil {
			t.Errorf("ParseDotted(%q): unexpected error %v", c.in, err)
			continue
		}
		if len(sel.Matchers) != c.matchers {
			t.Errorf("ParseDotted(%q): got %d matchers, want %d", c.in, len(sel.Matchers), c.matchers)
		}
	}
}

func TestParseDottedNamesPreserved(t *testing.T) {
	sel, err := ParseDotted(`{resource.host.id="42"}`)
	if err != nil {
		t.Fatalf("ParseDotted: %v", err)
	}
	if sel.Matchers[0].Name != "resource.host.id" {
		t.Fatalf("expected dotted name preserved, got %q", sel.Matchers[0].Name)
	}
}

func TestParseDottedInvalid(t *testing.T) {
	cases := []string{
		``,
		`{}`,                               // empty selector = match-all: rejected
		`resource.service.name="a"`,        // no braces
		`{resource.service.name}`,          // no op/value
		`{resource.service.name="a"`,       // unterminated
		`{resource.service.name="a"}extra`, // trailing input
		`{=""}`,                            // missing name
		`{.service.name="a"}`,              // name may not start with '.'
		`{resource.service.name=unquoted}`, // unquoted value
		`{resource.service.name=="a"}`,     // TraceQL '==' is not the canonical form
		`{resource.service.name="a" || resource.service.name="b"}`, // no boolean operators
	}
	for _, c := range cases {
		if _, err := ParseDotted(c); err == nil {
			t.Errorf("ParseDotted(%q): expected error, got nil", c)
		}
	}
}

func TestParseDottedMatcherSemantics(t *testing.T) {
	labels := map[string]string{
		"resource.service.name":           "checkout",
		"resource.deployment.environment": "prod",
	}

	cases := []struct {
		sel  string
		want bool
	}{
		{`{resource.service.name="checkout"}`, true},
		{`{resource.service.name="payments"}`, false},
		{`{resource.service.name!="payments"}`, true},
		{`{resource.service.name=~"check.*"}`, true},
		{`{resource.service.name=~"eckout"}`, false}, // anchored
		{`{resource.service.name!~"pay.*"}`, true},
		{`{resource.absent.label=""}`, true}, // absent → "" (Prometheus semantics)
		{`{resource.absent.label!=""}`, false},
		{`{resource.service.name="checkout",resource.deployment.environment="prod"}`, true},
		{`{resource.service.name="checkout",resource.deployment.environment="dev"}`, false},
	}
	for _, c := range cases {
		sel, err := ParseDotted(c.sel)
		if err != nil {
			t.Fatalf("ParseDotted(%q): %v", c.sel, err)
		}
		if got := sel.Matches(labels); got != c.want {
			t.Errorf("%q.Matches(...) = %v, want %v", c.sel, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Dialect divergence: Parse must reject dotted names, ParseDotted accepts them.
// ---------------------------------------------------------------------------

func TestDialectDots(t *testing.T) {
	if _, err := Parse(`{service.name="x"}`); err == nil {
		t.Error(`Parse must reject dotted label names`)
	}
	if _, err := ParseDotted(`{service.name="x"}`); err != nil {
		t.Errorf(`ParseDotted must accept dotted label names: %v`, err)
	}
}

// ---------------------------------------------------------------------------
// MatchValue — the single-value evaluation surface the metrics filter uses
// for __name__ matchers.
// ---------------------------------------------------------------------------

func TestMatchValue(t *testing.T) {
	sel := mustParse(t, `{__name__=~"node_.*",mode!="idle"}`)
	nameM, modeM := sel.Matchers[0], sel.Matchers[1]
	if !nameM.MatchValue("node_cpu_seconds_total") {
		t.Error("regex __name__ matcher must match node_cpu_seconds_total")
	}
	if nameM.MatchValue("process_cpu_seconds_total") {
		t.Error("regex __name__ matcher must not match process_cpu_seconds_total")
	}
	if modeM.MatchValue("idle") {
		t.Error(`mode!="idle" must not match "idle"`)
	}
	if !modeM.MatchValue("") {
		t.Error(`mode!="idle" must match an absent label ("")`)
	}
}
