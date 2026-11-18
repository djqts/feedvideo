package sql

import (
	"context"
	stdsql "database/sql"
	"gorm.io/gorm"
)

// CheckRequiredTables adds a read-only capability gate for facts outside this manifest.
func CheckRequiredTables(db *gorm.DB, names ...string) error {
	if db == nil {
		return migrationError("database_unavailable", "", 0)
	}
	return migrationConnection(db, func(ctx context.Context, c *stdsql.Conn) error {
		for _, name := range names {
			var count int
			if err := c.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?", name).Scan(&count); err != nil {
				return migrationError("schema_check_failed", name, 0)
			}
			if count != 1 {
				return migrationError("required_table_missing", name, 0)
			}
		}
		return nil
	})
}
