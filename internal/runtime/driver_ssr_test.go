package runtime

import "testing"

func TestCanHotReloadSSR(t *testing.T) {
	base := PortConfig{
		Port:          10001,
		Password:      "p",
		Method:        "chacha20-ietf",
		Protocol:      "auth_aes128_md5",
		ProtocolParam: "1:abc",
		Obfs:          "tls1.2_ticket_auth",
		ObfsParam:     "host.com",
		IsMultiUser:   true,
		ForbiddenIP:   "",
		ForbiddenPort: "",
	}

	changedUsers := base
	changedUsers.Users = map[int]string{1: "a", 2: "b"}
	if !canHotReloadSSR(base, changedUsers) {
		t.Fatalf("users-only change should support hot reload")
	}

	changedMethod := base
	changedMethod.Method = "aes-256-cfb"
	if canHotReloadSSR(base, changedMethod) {
		t.Fatalf("method change should not hot reload")
	}

	notMU := base
	notMU.IsMultiUser = false
	if canHotReloadSSR(base, notMU) {
		t.Fatalf("non-mu should not hot reload users")
	}
}
