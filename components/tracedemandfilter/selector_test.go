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

package leansignaltracedemandfilter

import "testing"

func TestParseSelectorValid(t *testing.T) {
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
		sel, err := parseSelector(c.in)
		if err != nil {
			t.Errorf("parseSelector(%q): unexpected error %v", c.in, err)
			continue
		}
		if len(sel.matchers) != c.matchers {
			t.Errorf("parseSelector(%q): got %d matchers, want %d", c.in, len(sel.matchers), c.matchers)
		}
	}
}

func TestParseSelectorDottedNames(t *testing.T) {
	sel, err := parseSelector(`{resource.host.id="42"}`)
	if err != nil {
		t.Fatalf("parseSelector: %v", err)
	}
	if sel.matchers[0].name != "resource.host.id" {
		t.Fatalf("expected dotted name preserved, got %q", sel.matchers[0].name)
	}
}

func TestParseSelectorInvalid(t *testing.T) {
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
		if _, err := parseSelector(c); err == nil {
			t.Errorf("parseSelector(%q): expected error, got nil", c)
		}
	}
}

func TestMatcherSemantics(t *testing.T) {
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
		sel, err := parseSelector(c.sel)
		if err != nil {
			t.Fatalf("parseSelector(%q): %v", c.sel, err)
		}
		if got := sel.matches(labels); got != c.want {
			t.Errorf("%q.matches(...) = %v, want %v", c.sel, got, c.want)
		}
	}
}
