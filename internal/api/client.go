package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/config"
	"github.com/jashok5/shadowsocks-go/internal/model"

	"go.uber.org/zap"
)

type Client struct {
	mu         sync.RWMutex
	httpClient *http.Client
	baseURL    string
	token      string
	nodeID     int
	retryMax   int
	backoff    time.Duration
	maxBackoff time.Duration
	log        *zap.Logger
}

func NewClient(httpClient *http.Client, cfg config.APIConfig) *Client {
	return &Client{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(cfg.URL, "/") + "/mod_mu",
		token:      cfg.Token,
		retryMax:   cfg.RetryMax,
		backoff:    cfg.RetryBackoff,
		maxBackoff: cfg.RetryMaxBackoff,
		log:        zap.NewNop(),
	}
}

func (c *Client) SetLogger(log *zap.Logger) {
	if log != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.log = log
	}
}

func (c *Client) SetNodeID(id int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nodeID = id
}

func (c *Client) UpdateRetryPolicy(max int, backoff, maxBackoff time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if max >= 0 {
		c.retryMax = max
	}
	if backoff > 0 {
		c.backoff = backoff
	}
	if maxBackoff >= c.backoff {
		c.maxBackoff = maxBackoff
	}
}

func (c *Client) Ping(ctx context.Context) error {
	var out string
	return c.get(ctx, "func/ping", nil, &out)
}

func (c *Client) GetNodeInfo(ctx context.Context) (model.NodeInfo, error) {
	nodeID := c.getNodeID()
	var out model.NodeInfo
	uri := "nodes/" + strconv.Itoa(nodeID) + "/info"
	params := map[string]string{"node_id": strconv.Itoa(nodeID)}
	err := c.get(ctx, uri, params, &out)
	return out, err
}

func (c *Client) GetUsers(ctx context.Context) ([]model.User, error) {
	nodeID := c.getNodeID()
	var out []model.User
	params := map[string]string{"node_id": strconv.Itoa(nodeID)}
	err := c.get(ctx, "users", params, &out)
	return out, err
}

func (c *Client) GetDetectRules(ctx context.Context) ([]model.DetectRule, error) {
	var out []model.DetectRule
	err := c.get(ctx, "func/detect_rules", nil, &out)
	return out, err
}

func (c *Client) GetNodes(ctx context.Context) ([]map[string]any, error) {
	var out []map[string]any
	err := c.get(ctx, "nodes", nil, &out)
	return out, err
}

func (c *Client) PostUserTraffic(ctx context.Context, data []model.UserTraffic) error {
	nodeID := c.getNodeID()
	body := map[string]any{"data": data}
	params := map[string]string{"node_id": strconv.Itoa(nodeID)}
	return c.post(ctx, "users/traffic", params, body)
}

func (c *Client) PostNodeInfo(ctx context.Context, uptime string, load string) error {
	nodeID := c.getNodeID()
	body := map[string]any{"uptime": uptime, "load": load}
	params := map[string]string{"node_id": strconv.Itoa(nodeID)}
	uri := "nodes/" + strconv.Itoa(nodeID) + "/info"
	return c.post(ctx, uri, params, body)
}

func (c *Client) PostAliveIP(ctx context.Context, data []model.AliveIP) error {
	nodeID := c.getNodeID()
	body := map[string]any{"data": data}
	params := map[string]string{"node_id": strconv.Itoa(nodeID)}
	return c.post(ctx, "users/aliveip", params, body)
}

func (c *Client) PostDetectLog(ctx context.Context, data []model.DetectLog) error {
	nodeID := c.getNodeID()
	body := map[string]any{"data": data}
	params := map[string]string{"node_id": strconv.Itoa(nodeID)}
	return c.post(ctx, "users/detectlog", params, body)
}

func (c *Client) PostBlockIP(ctx context.Context, ips []string) error {
	nodeID := c.getNodeID()
	if len(ips) == 0 {
		return nil
	}
	type blockItem struct {
		IP string `json:"ip"`
	}
	data := make([]blockItem, 0, len(ips))
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		data = append(data, blockItem{IP: ip})
	}
	if len(data) == 0 {
		return nil
	}
	body := map[string]any{"data": data}
	params := map[string]string{"node_id": strconv.Itoa(nodeID)}
	return c.post(ctx, "func/block_ip", params, body)
}

func (c *Client) GetBlockIP(ctx context.Context) ([]map[string]any, error) {
	var out []map[string]any
	err := c.get(ctx, "func/block_ip", nil, &out)
	return out, err
}

func (c *Client) GetUnblockIP(ctx context.Context) ([]map[string]any, error) {
	var out []map[string]any
	err := c.get(ctx, "func/unblock_ip", nil, &out)
	return out, err
}

func (c *Client) get(ctx context.Context, uri string, params map[string]string, out any) error {
	return c.doWithRetry(ctx, func(attemptCtx context.Context) error {
		fullURL, err := c.buildURL(uri, params)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, fullURL, nil)
		if err != nil {
			return fmt.Errorf("new request: %w", err)
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("request failed: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return classifyHTTPError(resp.StatusCode, string(body))
		}
		return parseResponse(resp.Body, out)
	})
}

func (c *Client) post(ctx context.Context, uri string, params map[string]string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	return c.doWithRetry(ctx, func(attemptCtx context.Context) error {
		fullURL, err := c.buildURL(uri, params)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, fullURL, bytes.NewReader(b))
		if err != nil {
			return fmt.Errorf("new request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("request failed: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return classifyHTTPError(resp.StatusCode, string(body))
		}
		var out any
		return parseResponse(resp.Body, &out)
	})
}

func (c *Client) buildURL(uri string, params map[string]string) (string, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base url: %w", err)
	}
	u.Path = path.Join(u.Path, uri)
	q := u.Query()
	q.Set("key", c.token)
	for k, v := range params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func parseResponse(r io.Reader, out any) error {
	var resp model.APIResponse[json.RawMessage]
	if err := json.NewDecoder(r).Decode(&resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if resp.Ret == 0 {
		return nonRetryableError{err: fmt.Errorf("api ret=0")}
	}
	if out == nil {
		return nil
	}
	if len(resp.Data) == 0 || string(resp.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(resp.Data, out); err != nil {
		return nonRetryableError{err: fmt.Errorf("decode data: %w", err)}
	}
	return nil
}

type nonRetryableError struct {
	err error
}

func (e nonRetryableError) Error() string { return e.err.Error() }

func (e nonRetryableError) Unwrap() error { return e.err }

func (c *Client) doWithRetry(ctx context.Context, fn func(context.Context) error) error {
	retryMax, backoff, maxBackoff, log := c.getRetryPolicy()
	maxAttempt := retryMax + 1
	if maxAttempt <= 0 {
		maxAttempt = 1
	}
	var last error
	for attempt := 1; attempt <= maxAttempt; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := fn(ctx)
		if err == nil {
			if attempt > 1 {
				log.Debug("api call succeeded after retry", zap.Int("attempt", attempt), zap.Int("max_attempt", maxAttempt))
			}
			return nil
		}
		last = err
		if !isRetryable(err) || attempt == maxAttempt {
			if !isRetryable(err) {
				log.Debug("api call not retryable", zap.Int("attempt", attempt), zap.Error(err))
			}
			break
		}
		wait := min(backoff*time.Duration(1<<(attempt-1)), maxBackoff)
		log.Debug("api call retrying", zap.Int("attempt", attempt), zap.Duration("wait", wait), zap.Error(err))
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return last
}

func (c *Client) getNodeID() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.nodeID
}

func (c *Client) getRetryPolicy() (int, time.Duration, time.Duration, *zap.Logger) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.retryMax, c.backoff, c.maxBackoff, c.log
}

func classifyHTTPError(code int, body string) error {
	body = strings.TrimSpace(body)
	err := fmt.Errorf("server status %d: %s", code, body)
	if code >= http.StatusInternalServerError || code == http.StatusTooManyRequests {
		return err
	}
	return nonRetryableError{err: err}
}

func isRetryable(err error) bool {
	var nre nonRetryableError
	return !errors.As(err, &nre)
}
