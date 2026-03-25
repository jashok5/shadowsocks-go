package bootstrap

import "flag"

type RuntimeFlags struct {
	ConfigPath      string
	LogLevel        string
	LogFormat       string
	ShowVersionOnly bool
}

func ParseFlags() RuntimeFlags {
	configPathFlag := flag.String("config", "configs/config.example.yaml", "配置文件路径")
	logLevelFlag := flag.String("log-level", "", "覆盖日志级别: debug/info/warn/error")
	logFormatFlag := flag.String("log-format", "", "覆盖日志格式: console/json")
	versionFlag := flag.Bool("version", false, "输出版本信息并退出")
	flag.Parse()
	return RuntimeFlags{
		ConfigPath:      *configPathFlag,
		LogLevel:        *logLevelFlag,
		LogFormat:       *logFormatFlag,
		ShowVersionOnly: *versionFlag,
	}
}
