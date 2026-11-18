package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"
)

func InitLogger(cfg LoggerConfig, esIndexer ESBulkIndexer) (*slog.Logger, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}

	levelVar := new(slog.LevelVar)
	levelVar.Set(level)

	writers := []io.Writer{}

	if cfg.FileDir != "" && cfg.FileName != "" {
		filePath := filepath.Join(cfg.FileDir, cfg.FileName)
		fileWriter := &lumberjack.Logger{
			Filename:   filePath,
			MaxSize:    cfg.MaxSize,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAge,
			Compress:   cfg.Compress,
			LocalTime:  true,
		}
		writers = append(writers, fileWriter)
	}

	if cfg.Stdout {
		writers = append(writers, os.Stdout)
	}

	if cfg.ES.Enabled && esIndexer != nil {
		esCfg := cfg.ES
		esCfg.Indexer = esIndexer
		esWriter := NewESWriter(esCfg)
		writers = append(writers, esWriter)
	}

	if len(writers) == 0 {
		writers = append(writers, io.Discard)
	}

	multiWriter := io.MultiWriter(writers...)
	opts := &slog.HandlerOptions{
		Level:     levelVar,
		AddSource: cfg.AddSource,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.TimeKey:
				return slog.Attr{Key: "timestamp", Value: slog.Int64Value(a.Value.Time().Unix())}
			case slog.LevelKey:
				return slog.Attr{Key: "severity", Value: a.Value}
			case slog.MessageKey:
				return slog.Attr{Key: "message", Value: a.Value}
			case "error":
				return slog.Attr{
					Key:   "error",
					Value: slog.StringValue(fmt.Sprintf("%v", a.Value.Any())),
				}
			}
			return a
		},
	}
	handler := slog.NewJSONHandler(multiWriter, opts)
	logger := slog.New(handler)
	slog.SetDefault(logger)

	return logger, nil
}

func parseLevel(levelStr string) (slog.Level, error) {
	switch levelStr {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("invalid level: %s", levelStr)
	}
}
