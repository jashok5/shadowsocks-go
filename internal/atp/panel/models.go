package panel

type envelope struct {
	Ret  int    `json:"ret"`
	Msg  string `json:"msg"`
	Data any    `json:"data"`
}

type NodeInfoResponse struct {
	Ret  int      `json:"ret"`
	Data NodeInfo `json:"data"`
}

type NodeInfo struct {
	NodeGroup      int     `json:"node_group"`
	NodeClass      int     `json:"node_class"`
	NodeSpeedlimit float64 `json:"node_speedlimit"`
	TrafficRate    float64 `json:"traffic_rate"`
	Sort           int     `json:"sort"`
	Server         string  `json:"server"`
}

type UsersResponse struct {
	Ret  int        `json:"ret"`
	Data []UserInfo `json:"data"`
}

type UserInfo struct {
	ID             int32   `json:"id"`
	UUID           string  `json:"uuid"`
	Passwd         string  `json:"passwd"`
	NodeSpeedlimit float64 `json:"node_speedlimit"`
	NodeConnector  int     `json:"node_connector"`
}

type NodeStatusRequest struct {
	Load   string `json:"load"`
	Uptime string `json:"uptime"`
}

type TrafficRecord struct {
	UserID int32 `json:"user_id"`
	U      int   `json:"u"`
	D      int   `json:"d"`
}

type TrafficRequest struct {
	Data []TrafficRecord `json:"data"`
}

type AliveIPRecord struct {
	IP     string `json:"ip"`
	UserID int32  `json:"user_id"`
}

type AliveIPRequest struct {
	Data []AliveIPRecord `json:"data"`
}

type DetectRule struct {
	ID    int32  `json:"id"`
	Name  string `json:"name"`
	Text  string `json:"text"`
	Regex string `json:"regex"`
	Type  int    `json:"type"`
}

type DetectRulesResponse struct {
	Ret  int          `json:"ret"`
	Data []DetectRule `json:"data"`
}

type DetectLogRecord struct {
	UserID int32 `json:"user_id"`
	ListID int32 `json:"list_id"`
}

type DetectLogRequest struct {
	Data []DetectLogRecord `json:"data"`
}
