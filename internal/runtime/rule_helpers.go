package runtime

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

func walkMatchedRules(payload string, buckets DetectBuckets, onHit func(ruleID int) bool) {
	for id, expr := range buckets.Text {
		if matchPattern(payload, expr) && !onHit(id) {
			return
		}
	}
	hx := strings.ToLower(fmt.Sprintf("%x", payload))
	for id, expr := range buckets.Hex {
		if matchPattern(hx, strings.ToLower(strings.TrimSpace(expr))) && !onHit(id) {
			return
		}
	}
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
	for _, v := range strings.Split(forbidden, ",") {
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
	for _, item := range strings.Split(forbidden, ",") {
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
