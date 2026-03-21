package transfer

import (
	"testing"

	"github.com/jashok5/shadowsocks-go/internal/config"
	"github.com/jashok5/shadowsocks-go/internal/model"
)

func TestApplySwitchRuleExpr(t *testing.T) {
	users := []model.User{
		{ID: 1, Port: 1001, Method: "aes-256-gcm", IsMultiUser: 0},
		{ID: 2, Port: 1002, Method: "chacha20-ietf", IsMultiUser: 1},
	}
	out, dropped, err := applySwitchRule(users, config.SwitchRuleConfig{
		Enabled: true,
		Mode:    "expr",
		Expr:    "is_multi_user==1 && method==chacha20-ietf",
	})
	if err != nil {
		t.Fatalf("applySwitchRule failed: %v", err)
	}
	if dropped != 1 || len(out) != 1 || out[0].ID != 2 {
		t.Fatalf("unexpected switch result: dropped=%d out=%+v", dropped, out)
	}
}

func TestApplySwitchRuleDisabled(t *testing.T) {
	users := []model.User{{ID: 1}, {ID: 2}}
	out, dropped, err := applySwitchRule(users, config.SwitchRuleConfig{})
	if err != nil {
		t.Fatalf("applySwitchRule disabled failed: %v", err)
	}
	if dropped != 0 || len(out) != 2 {
		t.Fatalf("unexpected disabled result: dropped=%d len=%d", dropped, len(out))
	}
}
