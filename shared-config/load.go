package config

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Load reads an explicit file, or the legacy search paths when path is empty.
// Only declared environment variables override YAML; even an explicit empty
// environment value overrides the file. Loading never changes AppConfig or the
// package-global Viper instance and never terminates the process.
func Load(path string, envLookup func(string) (string, bool)) (Config, error) {
	if envLookup == nil {
		envLookup = os.LookupEnv
	}
	v := viper.New()
	v.SetConfigType("yaml")
	if path != "" {
		v.SetConfigFile(path)
	} else {
		v.SetConfigName("config")
		v.AddConfigPath("shared-config/config")
		v.AddConfigPath("./config")
		v.AddConfigPath(".")
	}
	for key, value := range map[string]interface{}{
		"app.env": "development", "app.port": ":8080",
		"database.pool.maxIdleConns": 10, "database.pool.maxOpenConns": 100,
		"database.pool.connMaxLifetime": 60, "database.pool.connMaxIdleTime": 10,
		"elasticsearch.timeout": 30 * time.Second,
		"feed.pool_ttl":         300, "feed.recall_size": 200, "feed.dedup_max": 2000,
		"feed.dedup_ttl": 86400, "feed.build_timeout_ms": 500,
		"feed.weights.hotness": 0.5, "feed.weights.interest": 0.3, "feed.weights.recency": 0.2,
	} {
		v.SetDefault(key, value)
	}
	if err := v.ReadInConfig(); err != nil {
		// Decoder errors may contain scalar values, including credentials.
		return Config{}, fmt.Errorf("config: cannot read configuration file")
	}
	for _, binding := range []struct{ key, env string }{
		{"app.env", "APP_ENV"},
		{"database.drivers.mysql.password", "MYSQL_PASSWORD"},
		{"database.drivers.mysql.username", "MYSQL_USER"},
		{"database.drivers.mysql.dbName", "MYSQL_DATABASE"},
		{"redis.password", "REDIS_PASSWORD"}, {"redis.addr", "REDIS_ADDR"},
		{"MongoDB.uri", "MONGO_URI"}, {"MongoDB.database", "MONGO_DB"},
		{"rabbitmq.addr", "RABBITMQ_URL"}, {"rabbitmq.username", "RABBITMQ_USER"}, {"rabbitmq.password", "RABBITMQ_PASS"},
		{"secretKey", "JWT_SECRET_KEY"},
		{"storage.endpoint", "MINIO_ENDPOINT"}, {"storage.access_key", "MINIO_ACCESS_KEY"}, {"storage.secret_key", "MINIO_SECRET_KEY"},
		{"storage.video_bucket", "VIDEO_BUCKET"}, {"storage.cover_bucket", "COVER_BUCKET"},
	} {
		if value, ok := envLookup(binding.env); ok {
			v.Set(binding.key, value)
		}
	}
	if value, ok := envLookup("ADMIN_IDS"); ok {
		ids := make([]string, 0)
		for _, part := range strings.Split(value, ",") {
			if id := strings.TrimSpace(part); id != "" {
				ids = append(ids, id)
			}
		}
		v.Set("admin_ids", ids)
	}
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: cannot decode configuration fields")
	}
	if err := Validate(cfg, "gateway"); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate aggregates field paths and reasons, never configuration values.
// The gateway currently loads the complete configuration; workers can validate
// only their storage configuration by specifying their runtime role.
func Validate(cfg Config, runtimeRole string) error {
	var problems []string
	add := func(field, reason string) { problems = append(problems, field+": "+reason) }
	if cfg.App.Env != "development" && cfg.App.Env != "production" {
		add("app.env", "must be development or production")
	}
	if runtimeRole != "gateway" && runtimeRole != "media-worker" && runtimeRole != "notification-worker" {
		add("runtime_role", "unsupported")
	}
	if runtimeRole == "gateway" {
		port, err := strconv.Atoi(strings.TrimPrefix(cfg.App.Port, ":"))
		if err != nil || port < 1 || port > 65535 {
			add("app.port", "must be a port from 1 to 65535")
		}
		if _, err := cfg.DB.BuildDBDSNWithError(); err != nil {
			add("database.default", "requires a valid supported mysql driver")
		}
		pool := cfg.DB.Pool
		if pool.MaxOpenConns <= 0 || pool.MaxIdleConns < 0 || pool.MaxIdleConns > pool.MaxOpenConns {
			add("database.pool", "requires 0 <= maxIdleConns <= maxOpenConns and maxOpenConns > 0")
		}
		if pool.ConnMaxLifetime <= 0 || pool.ConnMaxIdleTime <= 0 {
			add("database.pool", "connection lifetimes must be positive")
		}
		if driver, ok := cfg.DB.Drivers[cfg.DB.Default]; ok && driver.PoolOverride != nil {
			p := driver.PoolOverride
			if p.MaxOpenConns <= 0 || p.MaxIdleConns < 0 || p.MaxIdleConns > p.MaxOpenConns || p.ConnMaxLifetime <= 0 || p.ConnMaxIdleTime <= 0 {
				add("database.drivers.mysql.poolOverride", "invalid connection pool")
			}
		}
		if cfg.ES.Timeout <= 0 {
			add("elasticsearch.timeout", "must be positive")
		}
		if cfg.Feed.PoolTTL <= 0 || cfg.Feed.DedupTTL <= 0 || cfg.Feed.BuildTimeoutMs <= 0 || cfg.Feed.RecallSize <= 0 || cfg.Feed.DedupMax <= 0 {
			add("feed", "sizes and timeouts must be positive")
		}
		w := cfg.Feed.Weights
		if w.Hotness < 0 || w.Interest < 0 || w.Recency < 0 || math.IsNaN(w.Hotness+w.Interest+w.Recency) || math.IsInf(w.Hotness+w.Interest+w.Recency, 0) || w.Hotness+w.Interest+w.Recency <= 0 {
			add("feed.weights", "must be finite, nonnegative and have a positive sum")
		}
	}
	if cfg.App.Env == "production" {
		for _, field := range []struct{ name, value string }{
			{"redis.password", cfg.Redis.Password}, {"MongoDB.uri", cfg.MongoDB.URI},
		} {
			if strings.TrimSpace(field.value) == "" {
				add(field.name, "required in production")
			}
		}
		if runtimeRole == "gateway" {
			driver := cfg.DB.Drivers[cfg.DB.Default]
			for _, field := range []struct{ name, value string }{
				{"database.drivers.mysql.username", driver.Username}, {"database.drivers.mysql.password", driver.Password},
				{"secretKey", cfg.SecretKey}, {"rabbitmq.addr", cfg.RabbitMQ.Addr},
				{"storage.access_key", cfg.Storage.AccessKey}, {"storage.secret_key", cfg.Storage.SecretKey},
			} {
				if strings.TrimSpace(field.value) == "" || field.value == "change-me" {
					add(field.name, "required independent production credential")
				}
			}
		}
		if runtimeRole == "media-worker" {
			if cfg.Storage.AccessKey == "" {
				add("storage.access_key", "required in production")
			}
			if cfg.Storage.SecretKey == "" {
				add("storage.secret_key", "required in production")
			}
		}
	}
	if len(problems) != 0 {
		return fmt.Errorf("invalid configuration: %s", strings.Join(problems, "; "))
	}
	return nil
}
