package elastic

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"feedvideo.local/shared-config"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/elastic/elastic-transport-go/v8/elastictransport"
	"github.com/elastic/go-elasticsearch/v8"
)

type ElasticClient struct {
	*elasticsearch.Client
}

func Init() (*elasticsearch.Client, error) {
	es, err := newConfiguredClient(false)
	if err != nil {
		return nil, err
	}
	client := &ElasticClient{Client: es}
	if err := client.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping Elasticsearch: %w", err)
	}
	slog.Info("Elasticsearch client initialized successfully", slog.Any("nodes", config.AppConfig.ES.Addresses))
	return es, nil
}

// InitProjection keeps a usable client when ES is temporarily offline at startup.
// Only configuration is validated here; the durable worker retries network failures.
func InitProjection() (*elasticsearch.Client, error) { return newConfiguredClient(true) }

func newConfiguredClient(projection bool) (*elasticsearch.Client, error) {
	if config.AppConfig == nil {
		return nil, fmt.Errorf("Elasticsearch configuration unavailable")
	}
	cfg := config.AppConfig.ES
	if projection && (len(cfg.Addresses) == 0 || cfg.Addresses[0] == "") {
		return nil, fmt.Errorf("projection requires Elasticsearch addresses")
	}
	esCfg := elasticsearch.Config{
		Addresses: cfg.Addresses,
		Username:  cfg.Username,
		Password:  cfg.Password,
		Transport: &http.Transport{
			MaxIdleConnsPerHost:   10,
			ResponseHeaderTimeout: cfg.Timeout,
			TLSClientConfig:       buildTLSConfig(cfg.CACert),
		},
		MaxRetries: cfg.MaxRetries,
	}

	if cfg.EnableDebug {
		esCfg.Logger = &elastictransport.JSONLogger{Output: os.Stdout}
	}

	es, err := elasticsearch.NewClient(esCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Elasticsearch client: %w", err)
	}
	return es, nil
}

func (c *ElasticClient) Ping() error {
	return c.PingContext(context.Background())
}

// PingContext keeps health probes within their caller's deadline.
func (c *ElasticClient) PingContext(ctx context.Context) error {
	res, err := c.Info(c.Info.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("info request: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("elasticsearch returned error: %s", res.Status())
	}

	var resp struct {
		ClusterName string `json:"cluster_name"`
		Version     struct {
			Number string `json:"number"`
		} `json:"version"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return fmt.Errorf("decode info response: %w", err)
	}
	if resp.ClusterName == "" {
		return fmt.Errorf("empty cluster name in info response")
	}
	slog.Info("connected to Elasticsearch",
		slog.String("cluster", resp.ClusterName),
		slog.String("version", resp.Version.Number),
	)
	return nil
}

func buildTLSConfig(caCert []byte) *tls.Config {
	if caCert == nil || len(caCert) == 0 {
		return nil
	}
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caCert) {
		slog.Warn("failed to append CA cert, using default TLS")
		return nil
	}
	return &tls.Config{
		RootCAs:    certPool,
		MinVersion: tls.VersionTLS12,
	}
}
