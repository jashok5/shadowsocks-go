package pipeline

type ProtocolPlugin interface {
	Name() string
	SetServerInfo(ServerInfo)
	ClientPreEncrypt([]byte) []byte
	ClientPostDecrypt([]byte) ([]byte, error)
	ServerPreEncrypt([]byte) []byte
	ServerPostDecrypt([]byte) ([]byte, bool, error)
	ServerUDPPreEncrypt([]byte, int) []byte
	ServerUDPPostDecrypt([]byte) ([]byte, int, error)
}

type ObfsPlugin interface {
	Name() string
	SetServerInfo(ServerInfo)
	ClientEncode([]byte) []byte
	ClientDecode([]byte) ([]byte, bool, error)
	ServerEncode([]byte) []byte
	ServerDecode([]byte) ([]byte, bool, bool, error)
	GetOverhead(bool) int
}
