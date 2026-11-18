package config

import (
	"fmt"
	"strings"
	"time"
)

// config 总配置
type Config struct {
	AdminIDs []string `mapstructure:"admin_ids"` // 全局管理员白名单（#2）
	App      struct {
		Name string `mapstructure:"name"`
		Port string `mapstructure:"port"`
		Env  string `mapstructure:"env"` // development / production；缺失时由 APP_ENV 或默认 development 决定
	}
	DB           DBConfig           `mapstructure:"database"`
	SecretKey    string             `mapstructure:"secretKey"`
	Redis        RedisConfig        `mapstructure:"redis"`
	MongoDB      MongoDBConfig      `mapstructure:"MongoDB"`
	ES           ESConfig           `mapstructure:"elasticsearch"`
	RabbitMQ     RabbitMQConfig     `mapstructure:"rabbitmq"`
	Kafka        KafkaConfig        `mapstructure:"kafka"`
	Feed         FeedConfig         `mapstructure:"feed"`
	Notification NotificationConfig `mapstructure:"notification"`
	Search       SearchConfig       `mapstructure:"search"`
	Creator      CreatorConfig      `mapstructure:"creator"`
	Storage      StorageConfig      `mapstructure:"storage"`
}

// SearchConfig 搜索模块配置。
type SearchConfig struct {
	CacheTTL         int `mapstructure:"cache_ttl"`
	HotMax           int `mapstructure:"hot_max"`
	SuggestMax       int `mapstructure:"suggest_max"`
	SuggestPrefixMin int `mapstructure:"suggest_prefix_min"`
}

// NotificationConfig 通知模块配置。AdminIDList 归一化管理员白名单为 []string。
type NotificationConfig struct {
	AdminIDs interface{} `mapstructure:"admin_ids"`
}

// AdminIDList 支持 YAML 数组 ["1","2"] 或逗号串 "1,2"；空/缺省 → nil。
func (n NotificationConfig) AdminIDList() []string {
	switch v := n.AdminIDs.(type) {
	case string:
		var out []string
		for _, part := range strings.Split(v, ",") {
			p := strings.TrimSpace(part)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	case []string:
		return v
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s := strings.TrimSpace(fmt.Sprintf("%v", item)); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// redis配置结构体
type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// MongoDB配置结构体
type MongoDBConfig struct {
	URI      string `mapstructure:"uri"`
	Database string `mapstructure:"database"`
}

// Elasticsearch配置结构体
type ESConfig struct {
	Addresses []string `mapstructure:"addresses"`
	Username  string   `mapstructure:"username"`
	Password  string   `mapstructure:"password"`
	CACert    []byte   `mapstructure:"ca_cert"`
	// 连接参数
	MaxRetries    int           `mapstructure:"max_retries"`
	RetryOnStatus []int         `mapstructure:"retry_on_status"`
	Timeout       time.Duration `mapstructure:"timeout"`
	//调试参数
	EnableDebug bool `mapstructure:"enable_debug"`
}

// RabbitMQ配置结构体
type RabbitMQConfig struct {
	Addr string `mapstructure:"addr"`

	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	VHost    string `mapstructure:"vhost"`
}

// Kafka配置结构体
type KafkaConfig struct {
	Brokers   []string `mapstructure:"brokers"`
	GroupID   string   `mapstructure:"group_id"`
	Topic     string   `mapstructure:"topic"`
	Partition int      `mapstructure:"partition"`
	Offset    int64    `mapstructure:"offset"`
}

// FeedConfig Feed 流配置
type FeedConfig struct {
	PoolTTL        int         `mapstructure:"pool_ttl"`         // 候选池逻辑过期秒数
	RecallSize     int64       `mapstructure:"recall_size"`      // 召回/候选池目标规模
	DedupMax       int64       `mapstructure:"dedup_max"`        // 去重 ZSet 保留上限
	DedupTTL       int         `mapstructure:"dedup_ttl"`        // 去重 ZSet TTL 秒数
	BuildTimeoutMs int         `mapstructure:"build_timeout_ms"` // 同步构建超时毫秒
	Weights        FeedWeights `mapstructure:"weights"`
}

// FeedWeights 打分权重（非负；和为 0 时回退默认 0.5/0.3/0.2）
type FeedWeights struct {
	Hotness  float64 `mapstructure:"hotness"`
	Interest float64 `mapstructure:"interest"`
	Recency  float64 `mapstructure:"recency"`
}

var AppConfig *Config

// InitConfig is a compatibility adapter. New entry points should call Load and
// handle its error before constructing dependencies.
func InitConfig() error {
	cfg, err := Load("", nil)
	if err != nil {
		return err
	}
	AppConfig = &cfg
	return nil
}

// CreatorConfig 创作者中心配置。
type CreatorConfig struct {
	CPM float64 `mapstructure:"cpm"`
}

// StorageConfig MinIO 存储配置。
type StorageConfig struct {
	Endpoint          string   `mapstructure:"endpoint"`
	AccessKey         string   `mapstructure:"access_key"`
	SecretKey         string   `mapstructure:"secret_key"`
	UseSSL            bool     `mapstructure:"use_ssl"`
	VideoBucket       string   `mapstructure:"video_bucket"`
	CoverBucket       string   `mapstructure:"cover_bucket"`
	MaxVideoSize      int64    `mapstructure:"max_video_size"`
	MaxCoverSize      int64    `mapstructure:"max_cover_size"`
	AllowedVideoTypes []string `mapstructure:"allowed_video_types"`
	AllowedCoverTypes []string `mapstructure:"allowed_cover_types"`
}

// ExtByMime 扩展名到 Content-Type 的映射表（hardcoded，不可配置）。
var ExtByMime = map[string]string{
	"video/mp4":       ".mp4",
	"video/quicktime": ".mov",
	"video/webm":      ".webm",
	"image/jpeg":      ".jpg",
	"image/png":       ".png",
	"image/webp":      ".webp",
}

// LoadStorageConfig 返回带默认值的 StorageConfig。
// 若 AppConfig 已加载则从中读取，否则返回仅含默认值的配置。
func LoadStorageConfig() StorageConfig {
	cfg := StorageConfig{
		Endpoint:          "minio:9000",
		AccessKey:         "minioadmin",
		SecretKey:         "minioadmin",
		UseSSL:            false,
		VideoBucket:       "feedvideo-videos",
		CoverBucket:       "feedvideo-covers",
		MaxVideoSize:      524288000,
		MaxCoverSize:      10485760,
		AllowedVideoTypes: []string{"video/mp4", "video/quicktime", "video/webm"},
		AllowedCoverTypes: []string{"image/jpeg", "image/png", "image/webp"},
	}
	if AppConfig != nil {
		sc := AppConfig.Storage
		if sc.Endpoint != "" {
			cfg.Endpoint = sc.Endpoint
		}
		if sc.AccessKey != "" {
			cfg.AccessKey = sc.AccessKey
		}
		if sc.SecretKey != "" {
			cfg.SecretKey = sc.SecretKey
		}
		if sc.UseSSL {
			cfg.UseSSL = sc.UseSSL
		}
		if sc.VideoBucket != "" {
			cfg.VideoBucket = sc.VideoBucket
		}
		if sc.CoverBucket != "" {
			cfg.CoverBucket = sc.CoverBucket
		}
		if sc.MaxVideoSize > 0 {
			cfg.MaxVideoSize = sc.MaxVideoSize
		}
		if sc.MaxCoverSize > 0 {
			cfg.MaxCoverSize = sc.MaxCoverSize
		}
		if len(sc.AllowedVideoTypes) > 0 {
			cfg.AllowedVideoTypes = sc.AllowedVideoTypes
		}
		if len(sc.AllowedCoverTypes) > 0 {
			cfg.AllowedCoverTypes = sc.AllowedCoverTypes
		}
	}
	return cfg
}
