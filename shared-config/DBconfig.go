package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

type DBConfig struct {
	Default string                    `mapstructure:"default"`
	Pool    PoolConfig                `mapstructure:"pool"`
	Drivers map[string]DBDriverConfig `mapstructure:"drivers"`
}

type PoolConfig struct {
	MaxIdleConns    int `mapstructure:"maxIdleConns"`
	MaxOpenConns    int `mapstructure:"maxOpenConns"`
	ConnMaxLifetime int `mapstructure:"connMaxLifetime"` // 单位分钟
	ConnMaxIdleTime int `mapstructure:"connMaxIdleTime"` // 单位分钟
}

type DBDriverConfig struct {
	// 通用字段
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	DBName   string `mapstructure:"dbName"`

	// 特定数据库的额外配置
	Charset   string `mapstructure:"charset,omitempty"`   // MySQL特有
	ParseTime bool   `mapstructure:"parseTime,omitempty"` // MySQL特有
	Loc       string `mapstructure:"loc,omitempty"`       // MySQL特有

	SSLMode string `mapstructure:"sslmode,omitempty"` // PostgreSQL特有

	Path string `mapstructure:"path,omitempty"` // SQLite特有

	// === 驱动级连接池覆盖（可选）===
	// 如果某些驱动需要特殊池配置，可在此覆盖全局 Pool
	PoolOverride *PoolConfig `mapstructure:"poolOverride"`
}

// BuildDBDSN 构建数据库连接字符串（DSN）
func (c *DBConfig) BuildDBDSN() string {
	dsn, _ := c.BuildDBDSNWithError()
	return dsn
}

// BuildDBDSNWithError delegates DSN encoding to the MySQL driver. Credentials
// must never be query-escaped or included in returned diagnostic errors.
func (c *DBConfig) BuildDBDSNWithError() (string, error) {
	if c == nil || c.Default != "mysql" {
		return "", fmt.Errorf("database.default: unsupported driver")
	}
	driver, ok := c.Drivers[c.Default]
	if !ok {
		return "", fmt.Errorf("database.drivers.mysql: missing driver configuration")
	}
	if strings.TrimSpace(driver.Host) == "" || driver.Port < 1 || driver.Port > 65535 || strings.TrimSpace(driver.DBName) == "" {
		return "", fmt.Errorf("database.drivers.mysql: host, valid port and dbName are required")
	}
	location := time.UTC
	if driver.Loc != "" {
		var err error
		location, err = time.LoadLocation(driver.Loc)
		if err != nil {
			return "", fmt.Errorf("database.drivers.mysql.loc: invalid time zone")
		}
	}
	dsn := mysql.NewConfig()
	dsn.User, dsn.Passwd = driver.Username, driver.Password
	dsn.Net = "tcp"
	dsn.Addr = net.JoinHostPort(strings.Trim(driver.Host, "[]"), strconv.Itoa(driver.Port))
	dsn.DBName, dsn.ParseTime, dsn.Loc = driver.DBName, driver.ParseTime, location
	if driver.Charset != "" {
		dsn.Params = map[string]string{"charset": driver.Charset}
	}
	return dsn.FormatDSN(), nil
}
