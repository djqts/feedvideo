package sql

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	config "feedvideo.local/shared-config"
	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// MigrationStartupPolicy requires explicit development opt-ins. Production only checks.
func MigrationStartupPolicy(environment string, lookup func(string) string) (string, bool, error) {
	if lookup == nil {
		lookup = os.Getenv
	}
	mode := lookup("MIGRATIONS_MODE")
	if mode == "" {
		mode = "check"
	}
	if mode != "check" && mode != "apply" && mode != "disabled" {
		return "", false, fmt.Errorf("invalid MIGRATIONS_MODE")
	}
	auto := false
	if value := lookup("DB_AUTO_MIGRATE"); value != "" {
		var err error
		auto, err = strconv.ParseBool(value)
		if err != nil {
			return "", false, fmt.Errorf("invalid DB_AUTO_MIGRATE")
		}
	}
	if strings.EqualFold(environment, "production") && (mode != "check" || auto) {
		return "", false, fmt.Errorf("production requires read-only schema checks")
	}
	return mode, auto, nil
}

// ConnectDB opens a configured pool without migrations. The command and gateway own policy.
func ConnectDB(cfg *config.Config) (*gorm.DB, error) {
	if cfg == nil {
		return nil, fmt.Errorf("database configuration unavailable")
	}
	if cfg.DB.Default != "mysql" {
		return nil, fmt.Errorf("unsupported database driver")
	}
	dsn, err := cfg.DB.BuildDBDSNWithError()
	if err != nil {
		return nil, fmt.Errorf("database configuration invalid")
	}
	parsed, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("database configuration invalid")
	}
	if parsed.Timeout == 0 {
		parsed.Timeout = 2 * time.Second
	}
	if parsed.ReadTimeout == 0 {
		parsed.ReadTimeout = 5 * time.Second
	}
	if parsed.WriteTimeout == 0 {
		parsed.WriteTimeout = 5 * time.Second
	}
	poolConfig := cfg.DB.Pool
	if poolConfig.MaxIdleConns < 0 || poolConfig.MaxOpenConns < 0 || poolConfig.ConnMaxLifetime < 0 || poolConfig.ConnMaxIdleTime < 0 {
		return nil, fmt.Errorf("database pool configuration invalid")
	}
	db, err := gorm.Open(mysql.New(mysql.Config{DSN: parsed.FormatDSN(), DefaultStringSize: 171}), &gorm.Config{
		SkipDefaultTransaction:                   false,
		NamingStrategy:                           schema.NamingStrategy{TablePrefix: "t_", SingularTable: false},
		DisableForeignKeyConstraintWhenMigrating: true,
		Logger:                                   logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("database connection failed")
	}
	pool, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("database pool unavailable")
	}
	pool.SetMaxIdleConns(poolConfig.MaxIdleConns)
	pool.SetMaxOpenConns(poolConfig.MaxOpenConns)
	pool.SetConnMaxLifetime(time.Duration(poolConfig.ConnMaxLifetime) * time.Minute)
	pool.SetConnMaxIdleTime(time.Duration(poolConfig.ConnMaxIdleTime) * time.Minute)
	return db, nil
}

// InitDBWithError closes the pool if the schema gate fails, allowing the process to exit.
func InitDBWithError() (*gorm.DB, error) {
	if config.AppConfig == nil {
		return nil, fmt.Errorf("database configuration unavailable")
	}
	mode, _, err := MigrationStartupPolicy(config.AppConfig.App.Env, os.Getenv)
	if err != nil {
		return nil, err
	}
	db, err := ConnectDB(config.AppConfig)
	if err != nil {
		return nil, err
	}
	switch mode {
	case "apply":
		err = RunMigrations(db, os.Getenv("MIGRATIONS_DIR"))
	case "check":
		err = CheckMigrations(db, os.Getenv("MIGRATIONS_DIR"))
	}
	if err != nil {
		if pool, poolErr := db.DB(); poolErr == nil {
			_ = pool.Close()
		}
		return nil, err
	}
	return db, nil
}

// InitDB is retained for source compatibility. New callers should handle InitDBWithError.
func InitDB() *gorm.DB {
	db, err := InitDBWithError()
	if err != nil {
		panic(err)
	}
	return db
}
