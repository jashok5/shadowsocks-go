package model

type APIResponse[T any] struct {
	Ret  int `json:"ret"`
	Data T   `json:"data"`
}

type NodeInfo struct {
	NodeGroup      int     `json:"node_group"`
	NodeClass      int     `json:"node_class"`
	NodeSpeedLimit float64 `json:"node_speedlimit"`
	TrafficRate    float64 `json:"traffic_rate"`
	MUOnly         int     `json:"mu_only"`
	Sort           int     `json:"sort"`
	Server         string  `json:"server"`
	PortOffset     int     `json:"port_offset"`
}

type User struct {
	ID            int     `json:"id"`
	UUID          string  `json:"uuid"`
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
	NodeConnector int     `json:"node_connector"`
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
