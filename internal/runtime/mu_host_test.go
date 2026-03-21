package runtime

import (
	"testing"

	"github.com/jashok5/shadowsocks-go/internal/model"
)

func TestGetMUHost(t *testing.T) {
	u := model.User{ID: 2, Passwd: "passwd", Method: "aes-256-gcm", Obfs: "tls1.2_ticket_auth", Protocol: "auth_aes128_md5"}
	host := getMUHost("%5m%id.%suffix", "example.com", u)
	if host != "648c82.example.com" {
		t.Fatalf("unexpected host: %s", host)
	}
}

func TestGetMUHostNegativeToken(t *testing.T) {
	u := model.User{ID: 2, Passwd: "passwd", Method: "aes-256-gcm", Obfs: "tls1.2_ticket_auth", Protocol: "auth_aes128_md5"}
	host := getMUHost("%-4m-%id", "ignored", u)
	if host != "d2b9-2" {
		t.Fatalf("unexpected host: %s", host)
	}
}

func TestCollectMUHosts(t *testing.T) {
	hosts := collectMUHosts(map[int]string{1: "b.example.com", 2: "a.example.com", 3: "a.example.com"})
	if len(hosts) != 2 {
		t.Fatalf("unexpected len: %d", len(hosts))
	}
	if hosts[0] != "a.example.com" || hosts[1] != "b.example.com" {
		t.Fatalf("unexpected hosts: %#v", hosts)
	}
}
