package server

import (
	"encoding/hex"

	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/common"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/common/log"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/core"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/utils/binaryx"
)

type ShadowsocksRProxy struct {
	Host          string            `json:"host,omitempty"`
	Port          int               `json:"port,omitempty"`
	Method        string            `json:"method,omitempty"`
	Password      string            `json:"password,omitempty"`
	Protocol      string            `json:"protocol,omitempty"`
	ProtocolParam string            `json:"protocolParam,omitempty"`
	Obfs          string            `json:"obfs,omitempty"`
	ObfsParam     string            `json:"obfsParam,omitempty"`
	Users         map[string]string `json:"users,omitempty"`
	Status        string            `json:"status,omitempty"`
	Single        int               `json:"single,omitempty"`
	core.HostFirewall
	common.TrafficReport `json:"-"`
	common.OnlineReport  `json:"-"`
}

func (ssr *ShadowsocksRProxy) AddUser(uid int, password string) {
	if ssr.Users == nil {
		ssr.Users = make(map[string]string)
	}
	uidPack := binaryx.LEUint32ToBytes(uint32(uid))
	log.Debugw("shadowsocksr add user", "uid_pack", hex.EncodeToString(uidPack), log.FieldUID, uid)
	uidPackStr := string(uidPack)
	ssr.Users[uidPackStr] = password
}

func (ssr *ShadowsocksRProxy) DelUser(uid int) {
	if ssr.Users == nil {
		return
	}
	uidPack := string(binaryx.LEUint32ToBytes(uint32(uid)))
	delete(ssr.Users, uidPack)
}

func (ssr *ShadowsocksRProxy) Reload(users map[string]string) {
	ssr.Users = users
}
