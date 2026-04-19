package initialize

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func NewLogger(level string, jsonOutput bool) (*zap.Logger, error) {
	encCfg := zap.NewProductionEncoderConfig()
	encCfg.TimeKey = "ts"
	encCfg.MessageKey = "msg"
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	logLevel := zap.InfoLevel
	if err := logLevel.UnmarshalText([]byte(level)); err != nil {
		return nil, err
	}

	encoding := "console"
	if jsonOutput {
		encoding = "json"
	}

	cfg := zap.Config{
		Level:             zap.NewAtomicLevelAt(logLevel),
		Development:       false,
		Encoding:          encoding,
		EncoderConfig:     encCfg,
		OutputPaths:       []string{"stdout"},
		ErrorOutputPaths:  []string{"stderr"},
		DisableCaller:     false,
		DisableStacktrace: true,
	}
	return cfg.Build()
}

func MustNewLogger(level string, jsonOutput bool) *zap.Logger {
	logger, err := NewLogger(level, jsonOutput)
	if err == nil {
		return logger
	}
	_, _ = os.Stderr.WriteString("invalid log level, fallback to info\n")
	fallback, _ := NewLogger("info", jsonOutput)
	return fallback
}

func ZapError(err error) zap.Field {
	return zap.Error(err)
}

func ZapString(key, value string) zap.Field {
	return zap.String(key, value)
}
