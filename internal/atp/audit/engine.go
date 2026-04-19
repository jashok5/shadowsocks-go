package audit

import (
	"net"
	"regexp"
	"strings"
	"sync/atomic"
)

type Rule struct {
	ID    int32
	Name  string
	Text  string
	Regex string
	Type  int
}

type compiledRule struct {
	id       int32
	ruleType int
	text     string
	re       *regexp.Regexp
}

type Target struct {
	Host     string
	Protocol string
}

type Engine struct {
	rules atomic.Value
}

type UpdateStats struct {
	Total       int
	Active      int
	RegexErrors int
}

func NewEngine() *Engine {
	e := &Engine{}
	e.rules.Store([]compiledRule{})
	return e
}

func (e *Engine) Update(raw []Rule) UpdateStats {
	stats := UpdateStats{Total: len(raw)}
	out := make([]compiledRule, 0, len(raw))
	for _, item := range raw {
		r := compiledRule{id: item.ID, ruleType: item.Type, text: strings.TrimSpace(item.Text)}
		regex := strings.TrimSpace(item.Regex)
		if regex != "" {
			re, err := regexp.Compile(regex)
			if err != nil {
				stats.RegexErrors++
				if r.text == "" {
					continue
				}
			} else {
				r.re = re
			}
		}
		out = append(out, r)
	}
	e.rules.Store(out)
	stats.Active = len(out)
	return stats
}

func (e *Engine) Match(target Target) []int32 {
	h := normalizeHost(target.Host)
	p := strings.ToLower(strings.TrimSpace(target.Protocol))
	rules := e.rules.Load().([]compiledRule)
	hits := make([]int32, 0, 2)
	for _, rule := range rules {
		switch rule.ruleType {
		case 1:
			if h != "" && matchDomain(rule, h) {
				hits = append(hits, rule.id)
			}
		case 2:
			if (h != "" && (matchRegex(rule, h) || matchText(rule, h))) || (p != "" && (matchRegex(rule, p) || matchText(rule, p))) {
				hits = append(hits, rule.id)
			}
		case 3:
			if p != "" && matchProtocol(rule, p) {
				hits = append(hits, rule.id)
			}
		default:
			if (h != "" && matchDomain(rule, h)) || (p != "" && matchProtocol(rule, p)) || (h != "" && matchRegex(rule, h)) {
				hits = append(hits, rule.id)
			}
		}
	}
	return hits
}

func matchDomain(rule compiledRule, host string) bool {
	if rule.re != nil && rule.re.MatchString(host) {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(rule.text))
	if text == "" {
		return false
	}
	return strings.Contains(host, text)
}

func matchRegex(rule compiledRule, value string) bool {
	if rule.re == nil {
		return false
	}
	return rule.re.MatchString(value)
}

func matchText(rule compiledRule, value string) bool {
	text := strings.ToLower(strings.TrimSpace(rule.text))
	if text == "" {
		return false
	}
	return strings.Contains(value, text)
}

func matchProtocol(rule compiledRule, protocol string) bool {
	if rule.re != nil && rule.re.MatchString(protocol) {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(rule.text))
	if text == "" {
		return false
	}
	return text == protocol
}

func normalizeHost(raw string) string {
	h := strings.ToLower(strings.TrimSpace(raw))
	if h == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.TrimPrefix(h, "[")
	h = strings.TrimSuffix(h, "]")
	h = strings.TrimSuffix(h, ".")
	return h
}
