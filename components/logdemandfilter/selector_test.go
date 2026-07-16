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

package leansignallogdemandfilter

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Parser — valid inputs
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
			sel, err := parseSelector(c.input)
			if err != nil {
				t.Fatalf("parseSelector(%q) failed: %v", c.input, err)
			}
			if len(sel.matchers) != c.matchers {
				t.Errorf("matchers: got %d want %d", len(sel.matchers), c.matchers)
			}
		})
	}
}

func TestParseSelectorUnquotesValues(t *testing.T) {
	sel, err := parseSelector(`{a="say \"hi\"", b="tab\there"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := sel.matchers[0].value; got != `say "hi"` {
		t.Errorf("escaped quotes: got %q", got)
	}
	if got := sel.matchers[1].value; got != "tab\there" {
		t.Errorf("escaped tab: got %q", got)
	}

	sel, err = parseSelector("{a=`no\\escape`}")
	if err != nil {
		t.Fatal(err)
	}
	if got := sel.matchers[0].value; got != `no\escape` {
		t.Errorf("raw string: got %q (backslash must stay literal)", got)
	}
}

func TestParseSelectorValueWithBraceAndComma(t *testing.T) {
	// '}' and ',' inside a quoted value must not terminate parsing.
	sel, err := parseSelector(`{a="x}y,z"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := sel.matchers[0].value; got != "x}y,z" {
		t.Errorf("value: got %q want %q", got, "x}y,z")
	}
}

// ---------------------------------------------------------------------------
// Parser — invalid inputs
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
			if sel, err := parseSelector(c.input); err == nil {
				t.Errorf("parseSelector(%q) = %+v, want error", c.input, sel)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Matching semantics (Prometheus rules)
// ---------------------------------------------------------------------------

func mustParse(t *testing.T, s string) *selector {
	t.Helper()
	sel, err := parseSelector(s)
	if err != nil {
		t.Fatalf("parseSelector(%q): %v", s, err)
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
			if got := mustParse(t, c.selector).matches(labels); got != c.want {
				t.Errorf("%s on %v = %v, want %v", c.selector, labels, got, c.want)
			}
		})
	}
}

func TestMatcherRegexAnchoringWithMetaChars(t *testing.T) {
	// "^(?:v)$" wrapping must not let a top-level alternation escape the anchor.
	sel := mustParse(t, `{a=~"x|y"}`)
	if sel.matches(map[string]string{"a": "xz", "service_name": "s"}) {
		t.Error(`{a=~"x|y"} must not match "xz" (alternation must stay anchored)`)
	}
	if !sel.matches(map[string]string{"a": "y", "service_name": "s"}) {
		t.Error(`{a=~"x|y"} must match "y"`)
	}
}
