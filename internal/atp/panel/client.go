package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	muKey   string
	http    *http.Client
}

func New(baseURL, muKey string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		muKey:   muKey,
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) GetNodeInfo(ctx context.Context, nodeID int32) (NodeInfo, error) {
	var out NodeInfoResponse
	err := c.getJSON(ctx, "/mod_mu/nodes/"+strconv.Itoa(int(nodeID))+"/info", nil, &out)
	if err != nil {
		return NodeInfo{}, err
	}
	if out.Ret != 1 {
		return NodeInfo{}, fmt.Errorf("get node info failed ret=%d", out.Ret)
	}
	return out.Data, nil
}

func (c *Client) GetUsers(ctx context.Context, nodeID int32) ([]UserInfo, error) {
	var out UsersResponse
	err := c.getJSON(ctx, "/mod_mu/users", map[string]string{"node_id": strconv.Itoa(int(nodeID))}, &out)
	if err != nil {
		return nil, err
	}
	if out.Ret != 1 {
		return nil, fmt.Errorf("get users failed ret=%d", out.Ret)
	}
	return out.Data, nil
}

func (c *Client) PostNodeStatus(ctx context.Context, nodeID int32, body NodeStatusRequest) error {
	return c.postJSON(ctx, "/mod_mu/nodes/"+strconv.Itoa(int(nodeID))+"/info", nil, body, nil)
}

func (c *Client) PostTraffic(ctx context.Context, nodeID int32, body TrafficRequest) error {
	return c.postJSON(ctx, "/mod_mu/users/traffic", map[string]string{"node_id": strconv.Itoa(int(nodeID))}, body, nil)
}

func (c *Client) PostAliveIP(ctx context.Context, nodeID int32, body AliveIPRequest) error {
	return c.postJSON(ctx, "/mod_mu/users/aliveip", map[string]string{"node_id": strconv.Itoa(int(nodeID))}, body, nil)
}

func (c *Client) GetDetectRules(ctx context.Context) ([]DetectRule, error) {
	var out DetectRulesResponse
	err := c.getJSON(ctx, "/mod_mu/func/detect_rules", nil, &out)
	if err != nil {
		return nil, err
	}
	if out.Ret != 1 {
		return nil, fmt.Errorf("get detect rules failed ret=%d", out.Ret)
	}
	return out.Data, nil
}

func (c *Client) PostDetectLog(ctx context.Context, nodeID int32, body DetectLogRequest) error {
	return c.postJSON(ctx, "/mod_mu/users/detectlog", map[string]string{"node_id": strconv.Itoa(int(nodeID))}, body, nil)
}

func (c *Client) getJSON(ctx context.Context, p string, q map[string]string, out any) error {
	req, err := c.newRequest(ctx, http.MethodGet, p, q, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("panel get status=%d body=%s", resp.StatusCode, string(b))
	}
	if err = json.NewDecoder(resp.Body).Decode(out); err != nil {
		return err
	}
	return nil
}

func (c *Client) postJSON(ctx context.Context, p string, q map[string]string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := c.newRequest(ctx, http.MethodPost, p, q, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		pb, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("panel post status=%d body=%s", resp.StatusCode, string(pb))
	}
	if out != nil {
		if err = json.NewDecoder(resp.Body).Decode(out); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) newRequest(ctx context.Context, method, p string, q map[string]string, body io.Reader) (*http.Request, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(u.Path, strings.TrimPrefix(p, "/"))
	query := u.Query()
	query.Set("key", c.muKey)
	for k, v := range q {
		query.Set(k, v)
	}
	u.RawQuery = query.Encode()
	return http.NewRequestWithContext(ctx, method, u.String(), body)
}
