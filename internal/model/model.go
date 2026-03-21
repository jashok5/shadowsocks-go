package model

type APIResponse[T any] struct {
	Ret  int `json:"ret"`
	Data T   `json:"data"`
}

type NodeInfo struct {
	NodeSpeedLimit float64 `json:"node_speedlimit"`
	TrafficRate    float64 `json:"traffic_rate"`
	MUOnly         int     `json:"mu_only"`
	PortOffset     int     `json:"port_offset"`
}

type User struct {
	ID            int     `json:"id"`
	Port          int     `json:"port"`
	Passwd        string  `json:"passwd"`
	Method        string  `json:"method"`
	Protocol      string  `json:"protocol"`
	ProtocolParam string  `json:"protocol_param"`
	Obfs          string  `json:"obfs"`
	ObfsParam     string  `json:"obfs_param"`
	ForbiddenIP   string  `json:"forbidden_ip"`
	ForbiddenPort string  `json:"forbidden_port"`
	NodeSpeed     float64 `json:"node_speedlimit"`
	IsMultiUser   int     `json:"is_multi_user"`
}

type DetectRule struct {
	ID    int    `json:"id"`
	Regex string `json:"regex"`
	Type  int    `json:"type"`
}

type UserTraffic struct {
	U      int64 `json:"u"`
	D      int64 `json:"d"`
	UserID int   `json:"user_id"`
}

type AliveIP struct {
	IP     string `json:"ip"`
	UserID int    `json:"user_id"`
}

type DetectLog struct {
	ListID int `json:"list_id"`
	UserID int `json:"user_id"`
}

type PortTransfer struct {
	Upload   int64
	Download int64
}
