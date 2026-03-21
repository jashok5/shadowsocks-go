package transfer

import (
	"fmt"
	"strings"

	"github.com/jashok5/shadowsocks-go/internal/config"
	"github.com/jashok5/shadowsocks-go/internal/model"
)

func applySwitchRule(users []model.User, cfg config.SwitchRuleConfig) ([]model.User, int, error) {
	if !cfg.Enabled {
		return users, 0, nil
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" || mode == "none" {
		return users, 0, nil
	}
	if mode != "expr" {
		return users, 0, fmt.Errorf("unsupported switchrule mode: %s", cfg.Mode)
	}
	predicates, err := parseSwitchExpr(cfg.Expr)
	if err != nil {
		return users, 0, err
	}
	out := make([]model.User, 0, len(users))
	dropped := 0
	for _, u := range users {
		if matchesSwitchPredicates(u, predicates) {
			out = append(out, u)
			continue
		}
		dropped++
	}
	return out, dropped, nil
}

type switchPredicate struct {
	field string
	op    string
	value string
}

func parseSwitchExpr(expr string) ([]switchPredicate, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("empty switchrule expression")
	}
	parts := strings.Split(expr, "&&")
	out := make([]switchPredicate, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		op := "=="
		idx := strings.Index(p, "==")
		if idx < 0 {
			op = "!="
			idx = strings.Index(p, "!=")
		}
		if idx <= 0 {
			return nil, fmt.Errorf("invalid predicate: %s", p)
		}
		field := strings.TrimSpace(p[:idx])
		value := strings.TrimSpace(p[idx+2:])
		value = strings.Trim(value, "'\"")
		if field == "" {
			return nil, fmt.Errorf("invalid predicate field: %s", p)
		}
		out = append(out, switchPredicate{field: strings.ToLower(field), op: op, value: value})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty switchrule predicates")
	}
	return out, nil
}

func matchesSwitchPredicates(u model.User, predicates []switchPredicate) bool {
	for _, p := range predicates {
		left := switchFieldValue(u, p.field)
		if p.op == "==" && left != p.value {
			return false
		}
		if p.op == "!=" && left == p.value {
			return false
		}
	}
	return true
}

func switchFieldValue(u model.User, field string) string {
	switch field {
	case "id":
		return fmt.Sprintf("%d", u.ID)
	case "port":
		return fmt.Sprintf("%d", u.Port)
	case "passwd", "password":
		return u.Passwd
	case "method":
		return u.Method
	case "obfs":
		return u.Obfs
	case "obfs_param":
		return u.ObfsParam
	case "protocol":
		return u.Protocol
	case "protocol_param":
		return u.ProtocolParam
	case "node_speedlimit":
		return fmt.Sprintf("%g", u.NodeSpeed)
	case "forbidden_ip":
		return u.ForbiddenIP
	case "forbidden_port":
		return u.ForbiddenPort
	case "is_multi_user":
		return fmt.Sprintf("%d", u.IsMultiUser)
	default:
		return ""
	}
}
