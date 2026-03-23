package runtime

import (
	"bytes"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

func walkMatchedRules(payload string, buckets DetectBuckets, onHit func(ruleID int) bool) {
	walkMatchedRulesBytes([]byte(payload), buckets, onHit)
}

func walkMatchedRulesBytes(payload []byte, buckets DetectBuckets, onHit func(ruleID int) bool) {
	if len(payload) == 0 {
		return
	}

	var payloadText string
	for id, expr := range buckets.Text {
		if re := buckets.TextCompiled[id]; re != nil {
			if re.Match(payload) && !onHit(id) {
				return
			}
			continue
		}
		if payloadText == "" {
			payloadText = string(payload)
		}
		if matchPattern(payloadText, expr) && !onHit(id) {
			return
		}
	}

	if len(buckets.Hex) == 0 {
		return
	}

	var hexPayload string
	for id, expr := range buckets.Hex {
		if lit := buckets.HexBytes[id]; len(lit) > 0 {
			if bytes.Contains(payload, lit) && !onHit(id) {
				return
			}
			continue
		}
		if lit := buckets.HexLiteral[id]; lit != "" {
			if hexPayload == "" {
				hexPayload = hex.EncodeToString(payload)
			}
			if strings.Contains(hexPayload, lit) && !onHit(id) {
				return
			}
			continue
		}
		if re := buckets.HexCompiled[id]; re != nil {
			if hexPayload == "" {
				hexPayload = hex.EncodeToString(payload)
			}
			if re.MatchString(hexPayload) && !onHit(id) {
				return
			}
			continue
		}
		if hexPayload == "" {
			hexPayload = hex.EncodeToString(payload)
		}
		if matchPattern(hexPayload, strings.ToLower(strings.TrimSpace(expr))) && !onHit(id) {
			return
		}
	}
}

func compileDetectBuckets(b DetectBuckets) DetectBuckets {
	b.TextCompiled = make(map[int]*regexp.Regexp, len(b.Text))
	b.HexCompiled = make(map[int]*regexp.Regexp, len(b.Hex))
	b.HexLiteral = make(map[int]string, len(b.Hex))
	b.HexBytes = make(map[int][]byte, len(b.Hex))

	for id, expr := range b.Text {
		expr = strings.TrimSpace(expr)
		if expr == "" {
			continue
		}
		re, err := regexp.Compile(expr)
		if err == nil {
			b.TextCompiled[id] = re
		}
	}

	for id, expr := range b.Hex {
		norm := strings.ToLower(strings.TrimSpace(expr))
		if norm == "" {
			continue
		}
		if regexp.QuoteMeta(norm) == norm {
			b.HexLiteral[id] = norm
			if len(norm)%2 == 0 {
				if raw, err := hex.DecodeString(norm); err == nil && len(raw) > 0 {
					b.HexBytes[id] = raw
				}
			}
			continue
		}
		re, err := regexp.Compile(norm)
		if err == nil {
			b.HexCompiled[id] = re
		}
	}

	return b
}

func matchPattern(input, expr string) bool {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return false
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return strings.Contains(input, expr)
	}
	return re.MatchString(input)
}

func blockedIP(ip string, forbidden string) bool {
	if strings.TrimSpace(forbidden) == "" {
		return false
	}
	for v := range strings.SplitSeq(forbidden, ",") {
		if strings.TrimSpace(v) == ip {
			return true
		}
	}
	return false
}

func blockedPort(port int, forbidden string) bool {
	if strings.TrimSpace(forbidden) == "" {
		return false
	}
	for item := range strings.SplitSeq(forbidden, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, "-") {
			parts := strings.SplitN(item, "-", 2)
			if len(parts) != 2 {
				continue
			}
			l, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
			r, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err1 != nil || err2 != nil {
				continue
			}
			if l <= port && port <= r {
				return true
			}
			continue
		}
		p, err := strconv.Atoi(item)
		if err == nil && p == port {
			return true
		}
	}
	return false
}
