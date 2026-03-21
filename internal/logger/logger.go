package logger

import (
	"fmt"
	"os"
	"strings"

	"github.com/jashok5/shadowsocks-go/internal/config"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func New(cfg config.LogConfig) (*zap.Logger, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}

	encCfg := zap.NewProductionEncoderConfig()
	encCfg.TimeKey = "ts"
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	var enc zapcore.Encoder
	if strings.EqualFold(cfg.Format, "json") {
		enc = zapcore.NewJSONEncoder(encCfg)
	} else {
		enc = zapcore.NewConsoleEncoder(encCfg)
	}

	core := zapcore.NewCore(enc, zapcore.AddSync(os.Stdout), level)
	return zap.New(core, zap.AddCaller()), nil
}

func Err(err error) zap.Field {
	return zap.Error(err)
}

func parseLevel(v string) (zapcore.Level, error) {
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(strings.ToLower(strings.TrimSpace(v)))); err != nil {
		return zapcore.InfoLevel, fmt.Errorf("invalid log.level: %w", err)
	}
	return level, nil
}
