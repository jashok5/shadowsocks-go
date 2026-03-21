package plain

import "github.com/jashok5/shadowsocks-go/internal/ssr/pipeline"

type Protocol struct {
	serverInfo pipeline.ServerInfo
}

func NewProtocol() *Protocol { return &Protocol{} }

func (p *Protocol) Name() string { return "origin" }

func (p *Protocol) SetServerInfo(si pipeline.ServerInfo) { p.serverInfo = si }

func (p *Protocol) ClientPreEncrypt(b []byte) []byte { return b }

func (p *Protocol) ClientPostDecrypt(b []byte) ([]byte, error) { return b, nil }

func (p *Protocol) ServerPreEncrypt(b []byte) []byte { return b }

func (p *Protocol) ServerPostDecrypt(b []byte) ([]byte, bool, error) { return b, false, nil }

func (p *Protocol) ServerUDPPreEncrypt(b []byte, _ int) []byte { return b }

func (p *Protocol) ServerUDPPostDecrypt(b []byte) ([]byte, int, error) { return b, 0, nil }

type Obfs struct {
	serverInfo pipeline.ServerInfo
}

func NewObfs() *Obfs { return &Obfs{} }

func (o *Obfs) Name() string { return "plain" }

func (o *Obfs) SetServerInfo(si pipeline.ServerInfo) { o.serverInfo = si }

func (o *Obfs) ClientEncode(b []byte) []byte { return b }

func (o *Obfs) ClientDecode(b []byte) ([]byte, bool, error) { return b, false, nil }

func (o *Obfs) ServerEncode(b []byte) []byte { return b }

func (o *Obfs) ServerDecode(b []byte) ([]byte, bool, bool, error) { return b, true, false, nil }

func (o *Obfs) GetOverhead(_ bool) int { return 0 }
