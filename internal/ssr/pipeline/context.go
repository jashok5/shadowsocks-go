package pipeline

type ServerInfo struct {
	Host          string
	Port          int
	ProtocolParam string
	ObfsParam     string
	Password      string
	Method        string
	UsersTable    map[int]UserEntry
	IsMultiUser   bool
}

type UserEntry struct {
	UserID   int
	Password string
	Method   string
	Protocol string
	Obfs     string
}
