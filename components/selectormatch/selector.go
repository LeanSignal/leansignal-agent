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

// Package leansignalselectormatch is a small parser + evaluator for the
// canonical normalized label-selector grammar that lean-api uses for ALL
// demand domains (LogQL stream selectors for logs, resource selectors for
// traces, metric series selectors for metrics):
//
//	selector = "{" matcher ("," matcher)* "}"
//	matcher  = label_name op quoted_value
//	op       = "=" | "!=" | "=~" | "!~"
//
// Values are Go-quoted strings (unquoted with strconv.Unquote). Regex values
// are RE2, compiled fully anchored as ^(?:value)$ — Prometheus semantics.
// Matching semantics follow Prometheus exactly: an absent label is treated as
// the empty string, so `label=""` — and any regex that matches the empty
// string — matches a label set without that label.
//
// Two dialects differ ONLY in the label-name alphabet:
//   - Parse:       Prometheus label names [a-zA-Z_][a-zA-Z0-9_]*
//     (logs + metrics selectors)
//   - ParseDotted: dotted attribute keys [a-zA-Z_][a-zA-Z0-9_.]*
//     (trace resource selectors, e.g. `resource.service.name`)
//
// Hand-rolling this tiny grammar avoids a dependency on the
// prometheus/prometheus module.
package leansignalselectormatch

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Op is a label-matcher operator.
type Op int

const (
	OpEq  Op = iota // =
	OpNeq           // !=
	OpRe            // =~
	OpNre           // !~
)

// Matcher is one parsed label matcher (label_name op "value").
type Matcher struct {
	Name  string
	Op    Op
	Value string

	re *regexp.Regexp // compiled fully anchored ^(?:Value)$; set for OpRe/OpNre
}

// MatchValue evaluates the matcher against a single label value (the value of
// the label named Matcher.Name; pass "" for an absent label — Prometheus
// semantics).
func (m *Matcher) MatchValue(v string) bool {
	switch m.Op {
	case OpEq:
		return v == m.Value
	case OpNeq:
		return v != m.Value
	case OpRe:
		return m.re.MatchString(v)
	case OpNre:
		return !m.re.MatchString(v)
	}
	return false
}

// Matches evaluates the matcher against the given label set with Prometheus
// semantics: an absent label is treated as the empty string, so `label=""` —
// and any regex that matches the empty string — matches a label set without
// that label.
func (m *Matcher) Matches(labels map[string]string) bool {
	return m.MatchValue(labels[m.Name]) // absent → ""
}

// Selector is one parsed selector; ALL of its matchers must match.
type Selector struct {
	Matchers []*Matcher
}

// Matches reports whether every matcher in the selector matches the labels.
func (s *Selector) Matches(labels map[string]string) bool {
	for _, m := range s.Matchers {
		if !m.Matches(labels) {
			return false
		}
	}
	return true
}

// Parse parses the canonical normalized selector grammar with Prometheus
// label names ([a-zA-Z_][a-zA-Z0-9_]*). The empty selector "{}" is rejected:
// it would match everything, which must never happen implicitly in a
// block-all-by-default filter.
func Parse(input string) (*Selector, error) {
	return parse(input, false)
}

// ParseDotted parses the same grammar with the label-name alphabet widened
// with '.' ([a-zA-Z_][a-zA-Z0-9_.]*), so TraceQL resource-scoped attribute
// keys like `resource.service.name` are valid names.
func ParseDotted(input string) (*Selector, error) {
	return parse(input, true)
}

func parse(input string, allowDots bool) (*Selector, error) {
	p := &parser{s: input, allowDots: allowDots}
	p.skipSpace()
	if !p.consume('{') {
		return nil, fmt.Errorf("selector must start with '{'")
	}
	var ms []*Matcher
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
	return &Selector{Matchers: ms}, nil
}

// parser is a minimal byte scanner over one selector string.
type parser struct {
	s         string
	pos       int
	allowDots bool
}

func (p *parser) eof() bool { return p.pos >= len(p.s) }

func (p *parser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.s[p.pos]
}

func (p *parser) consume(c byte) bool {
	if !p.eof() && p.s[p.pos] == c {
		p.pos++
		return true
	}
	return false
}

func (p *parser) skipSpace() {
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
func (p *parser) parseMatcher() (*Matcher, error) {
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

	m := &Matcher{Name: name, Op: op, Value: value}
	if op == OpRe || op == OpNre {
		re, err := regexp.Compile("^(?:" + value + ")$")
		if err != nil {
			return nil, fmt.Errorf("invalid regex %q: %w", value, err)
		}
		m.re = re
	}
	return m, nil
}

// parseLabelName scans a label name: [a-zA-Z_][a-zA-Z0-9_]*, widened with '.'
// after the first character when allowDots is set.
func (p *parser) parseLabelName() (string, error) {
	start := p.pos
	for !p.eof() {
		c := p.s[p.pos]
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(p.pos > start && (c >= '0' && c <= '9')) ||
			(p.pos > start && p.allowDots && c == '.') {
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
func (p *parser) parseOp() (Op, error) {
	rest := p.s[p.pos:]
	switch {
	case strings.HasPrefix(rest, "=~"):
		p.pos += 2
		return OpRe, nil
	case strings.HasPrefix(rest, "!~"):
		p.pos += 2
		return OpNre, nil
	case strings.HasPrefix(rest, "!="):
		p.pos += 2
		return OpNeq, nil
	case strings.HasPrefix(rest, "="):
		p.pos++
		return OpEq, nil
	}
	return 0, fmt.Errorf("expected matcher operator (=, !=, =~, !~) at position %d", p.pos)
}

// parseQuotedValue scans a Go-quoted string token — double-quoted with
// backslash escapes, or backquoted raw — and unquotes it with strconv.Unquote.
func (p *parser) parseQuotedValue() (string, error) {
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
