package bootstrap

import (
	"context"
	"strings"

	"github.com/jashok5/shadowsocks-go/internal/api"
	"github.com/jashok5/shadowsocks-go/internal/config"
	"github.com/jashok5/shadowsocks-go/internal/model"
	"go.uber.org/zap"
)

func ResolveRuntimeDriver(ctx context.Context, cfg config.Config, apiClient *api.Client, log *zap.Logger) (string, model.NodeInfo, error) {
	driver := strings.ToLower(strings.TrimSpace(cfg.RT.Driver))
	if driver == "" {
		driver = "auto"
	}
	if driver == "atp" {
		nodeInfo, err := apiClient.GetNodeInfo(ctx)
		if err != nil {
			return "", model.NodeInfo{}, err
		}
		return driver, nodeInfo, nil
	}
	if driver != "auto" {
		return driver, model.NodeInfo{}, nil
	}
	nodeInfo, err := apiClient.GetNodeInfo(ctx)
	if err != nil {
		return "", model.NodeInfo{}, err
	}
	resolved := "ssr"
	if nodeInfo.Sort == 16 {
		resolved = "atp"
	}
	log.Info("runtime driver resolved by node sort",
		zap.Int("node_id", cfg.Node.ID),
		zap.Int("node_sort", nodeInfo.Sort),
		zap.String("runtime_driver", resolved),
	)
	return resolved, nodeInfo, nil
}
