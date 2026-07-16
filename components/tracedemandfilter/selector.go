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

// leansignaltracedemandfilter/selector.go
//
// A small parser + evaluator for trace resource selectors. lean-api sends
// selectors in a canonical normalized form; the grammar and matching
// semantics are the LogQL-stream-selector grammar of
// components/logdemandfilter/selector.go with ONE widening: label names may
// contain dots, because they are TraceQL resource-scoped attribute keys
// (e.g. `resource.service.name`). Matching semantics follow Prometheus
// exactly. Hand-rolling this tiny grammar avoids a dependency on the
// prometheus/prometheus module.
package leansignaltracedemandfilter

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// matchOp is a label-matcher operator.
type matchOp int

const (
	opEq  matchOp = iota // =
	opNeq                // !=
	opRe                 // =~
	opNre                // !~
)

// matcher is one parsed label matcher (label_name op "value").
type matcher struct {
	name  string
	op    matchOp
	value string
	re    *regexp.Regexp // compiled fully anchored ^(?:value)$; set for opRe/opNre
}

// matches evaluates the matcher against the given resource labels with
// Prometheus semantics: an absent label is treated as the empty string, so
// `label=""` — and any regex that matches the empty string — matches a
// resource without that attribute.
func (m *matcher) matches(labels map[string]string) bool {
	v := labels[m.name] // absent → ""
	switch m.op {
	case opEq:
		return v == m.value
	case opNeq:
		return v != m.value
	case opRe:
		return m.re.MatchString(v)
	case opNre:
		return !m.re.MatchString(v)
	}
	return false
}

// selector is one parsed resource selector; ALL of its matchers must match.
type selector struct {
	matchers []*matcher
}

// matches reports whether every matcher in the selector matches the labels.
func (s *selector) matches(labels map[string]string) bool {
	for _, m := range s.matchers {
		if !m.matches(labels) {
			return false
		}
	}
	return true
}

// parseSelector parses the canonical normalized resource-selector grammar:
//
//	selector = "{" matcher ("," matcher)* "}"
//	matcher  = label_name op quoted_value
//	op       = "=" | "!=" | "=~" | "!~"
//
// Values are Go-quoted strings (unquoted with strconv.Unquote). Regex values
// are RE2, compiled fully anchored as ^(?:value)$ — Prometheus semantics.
// The empty selector "{}" is rejected: it would match every resource, which
// must never happen implicitly in a block-all-by-default filter.
func parseSelector(input string) (*selector, error) {
	p := &selectorParser{s: input}
	p.skipSpace()
	if !p.consume('{') {
		return nil, fmt.Errorf("selector must start with '{'")
	}
	var ms []*matcher
	for {
		p.skipSpace()
		if p.peek() == '}' { // tolerates a trailing comma; "{}" rejected below
			break
		}
		m, err := p.parseMatcher()
		if err != nil {
			return nil, err
		}
		ms = append(ms, m)
		p.skipSpace()
		if p.consume(',') {
			continue
		}
		break
	}
	p.skipSpace()
	if !p.consume('}') {
		return nil, fmt.Errorf("expected ',' or '}' at position %d", p.pos)
	}
	p.skipSpace()
	if !p.eof() {
		return nil, fmt.Errorf("unexpected trailing input at position %d", p.pos)
	}
	if len(ms) == 0 {
		return nil, fmt.Errorf("empty selector {} is not allowed")
	}
	return &selector{matchers: ms}, nil
}

// selectorParser is a minimal byte scanner over one selector string.
type selectorParser struct {
	s   string
	pos int
}

func (p *selectorParser) eof() bool { return p.pos >= len(p.s) }

func (p *selectorParser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.s[p.pos]
}

func (p *selectorParser) consume(c byte) bool {
	if !p.eof() && p.s[p.pos] == c {
		p.pos++
		return true
	}
	return false
}

func (p *selectorParser) skipSpace() {
	for !p.eof() {
		switch p.s[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

// parseMatcher parses one `label_name op quoted_value` clause.
func (p *selectorParser) parseMatcher() (*matcher, error) {
	name, err := p.parseLabelName()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	op, err := p.parseOp()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	value, err := p.parseQuotedValue()
	if err != nil {
		return nil, err
	}

	m := &matcher{name: name, op: op, value: value}
	if op == opRe || op == opNre {
		re, err := regexp.Compile("^(?:" + value + ")$")
		if err != nil {
			return nil, fmt.Errorf("invalid regex %q: %w", value, err)
		}
		m.re = re
	}
	return m, nil
}

// parseLabelName scans a dotted attribute-key label name:
// [a-zA-Z_][a-zA-Z0-9_.]* — the LogQL grammar widened with '.' so TraceQL
// resource-scoped keys like `resource.service.name` are valid names.
func (p *selectorParser) parseLabelName() (string, error) {
	start := p.pos
	for !p.eof() {
		c := p.s[p.pos]
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(p.pos > start && (c == '.' || (c >= '0' && c <= '9'))) {
			p.pos++
			continue
		}
		break
	}
	if p.pos == start {
		return "", fmt.Errorf("expected label name at position %d", p.pos)
	}
	return p.s[start:p.pos], nil
}

// parseOp scans one of =, !=, =~, !~ (longest match first).
func (p *selectorParser) parseOp() (matchOp, error) {
	rest := p.s[p.pos:]
	switch {
	case strings.HasPrefix(rest, "=~"):
		p.pos += 2
		return opRe, nil
	case strings.HasPrefix(rest, "!~"):
		p.pos += 2
		return opNre, nil
	case strings.HasPrefix(rest, "!="):
		p.pos += 2
		return opNeq, nil
	case strings.HasPrefix(rest, "="):
		p.pos++
		return opEq, nil
	}
	return 0, fmt.Errorf("expected matcher operator (=, !=, =~, !~) at position %d", p.pos)
}

// parseQuotedValue scans a Go-quoted string token — double-quoted with
// backslash escapes, or backquoted raw — and unquotes it with strconv.Unquote.
func (p *selectorParser) parseQuotedValue() (string, error) {
	start := p.pos
	switch p.peek() {
	case '"':
		p.pos++
		for !p.eof() {
			switch p.s[p.pos] {
			case '\\':
				p.pos += 2 // skip the escape pair; overshoot ends as unterminated
				continue
			case '"':
				p.pos++
				return strconv.Unquote(p.s[start:p.pos])
			}
			p.pos++
		}
		return "", fmt.Errorf("unterminated quoted value at position %d", start)
	case '`':
		p.pos++
		for !p.eof() {
			if p.s[p.pos] == '`' {
				p.pos++
				return strconv.Unquote(p.s[start:p.pos])
			}
			p.pos++
		}
		return "", fmt.Errorf("unterminated raw value at position %d", start)
	}
	return "", fmt.Errorf("expected quoted value at position %d", p.pos)
}
