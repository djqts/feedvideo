package logger

// LoggerConfig 日志配置
type LoggerConfig struct {
	Level      string `yaml:"level"`       // debug, info, warn, error
	FileDir    string `yaml:"file_dir"`    // 日志目录，如 /var/log/app
	FileName   string `yaml:"file_name"`   // 文件名，如 app.log
	MaxSize    int    `yaml:"max_size"`    // 单文件最大大小（MB）
	MaxBackups int    `yaml:"max_backups"` // 保留的最大备份数
	MaxAge     int    `yaml:"max_age"`     // 保留的最大天数
	Compress   bool   `yaml:"compress"`    // 是否压缩历史日志
	Stdout     bool   `yaml:"stdout"`      // 是否同时输出到标准输出（开发用）
	AddSource  bool   `yaml:"add_source"`  // 是否记录调用源码位置
	ES         ESConfig
}


// 默认日志配置
var DefaultLogCfg = LoggerConfig{
    Level:      "info",
    FileDir:    "./logs",
    FileName:   "app.log",
    MaxSize:    100,
    MaxBackups: 10,
    MaxAge:     7,
    Compress:   true,
    Stdout:     false,
    AddSource:  true,
}