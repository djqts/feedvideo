package sql

import (
	"context"
	"crypto/sha256"
	stdsql "database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

const MigrationAppVersion = "0.1.0"

type Migration struct {
	Version          int    `json:"version"`
	Name             string `json:"name"`
	Direction        string `json:"direction"`
	Checksum         string `json:"checksum"`
	CompatibleAppMin string `json:"compatible_app_min"`
	statements       []string
}
type migrationManifest struct {
	SchemaVersion int         `json:"schema_version"`
	Migrations    []Migration `json:"migrations"`
}

// MigrationError omits driver errors and SQL text, which can contain credentials.
type MigrationError struct {
	Code, Name string
	Step       int
}

func (e *MigrationError) Error() string {
	return fmt.Sprintf("migration %s: %s (step %d)", e.Name, e.Code, e.Step)
}
func migrationError(code, name string, step int) error {
	return &MigrationError{Code: code, Name: name, Step: step}
}

// LoadMigrationPlan validates every listed artifact and returns only explicit up entries.
func LoadMigrationPlan(dir string) ([]Migration, error) {
	if dir == "" {
		dir = "migrations"
	}
	f, err := os.Open(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, migrationError("manifest_unavailable", "manifest.json", 0)
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 1<<20))
	dec.DisallowUnknownFields()
	var manifest migrationManifest
	if err = dec.Decode(&manifest); err != nil {
		return nil, migrationError("manifest_invalid", "manifest.json", 0)
	}
	if err = dec.Decode(&struct{}{}); err != io.EOF || manifest.SchemaVersion != 1 || len(manifest.Migrations) == 0 {
		return nil, migrationError("manifest_invalid", "manifest.json", 0)
	}
	names := map[string]bool{}
	versions := map[string]bool{}
	var up []Migration
	for _, m := range manifest.Migrations {
		if m.Name == "" || filepath.Base(m.Name) != m.Name || strings.ContainsAny(m.Name, "/\\") || !strings.HasSuffix(m.Name, ".sql") || m.Version <= 0 || (m.Direction != "up" && m.Direction != "down") || m.CompatibleAppMin == "" {
			return nil, migrationError("entry_invalid", "manifest.json", 0)
		}
		key := fmt.Sprintf("%d:%s", m.Version, m.Direction)
		if names[m.Name] || versions[key] {
			return nil, migrationError("entry_duplicate", m.Name, 0)
		}
		names[m.Name] = true
		versions[key] = true
		if _, err := versionParts(m.CompatibleAppMin); err != nil {
			return nil, migrationError("app_version_invalid", m.Name, 0)
		}
		info, err := os.Lstat(filepath.Join(dir, m.Name))
		if err != nil || !info.Mode().IsRegular() {
			return nil, migrationError("artifact_unavailable", m.Name, 0)
		}
		content, err := os.ReadFile(filepath.Join(dir, m.Name))
		if err != nil {
			return nil, migrationError("artifact_unavailable", m.Name, 0)
		}
		digest := sha256.Sum256(content)
		encoded, err := hex.DecodeString(m.Checksum)
		if err != nil || len(encoded) != 32 || !strings.EqualFold(m.Checksum, hex.EncodeToString(digest[:])) {
			return nil, migrationError("checksum_mismatch", m.Name, 0)
		}
		m.Checksum = strings.ToLower(m.Checksum)
		if m.Direction == "up" {
			if newerVersion(m.CompatibleAppMin, MigrationAppVersion) {
				return nil, migrationError("app_version_unsupported", m.Name, 0)
			}
			m.statements, err = splitMigrationSQL(string(content))
			if err != nil {
				return nil, migrationError("sql_invalid", m.Name, 0)
			}
			if len(m.statements) == 0 {
				return nil, migrationError("sql_empty", m.Name, 0)
			}
			up = append(up, m)
		}
	}
	sort.Slice(up, func(i, j int) bool { return up[i].Version < up[j].Version })
	return up, nil
}
func versionParts(v string) ([3]int, error) {
	var out [3]int
	fields := strings.Split(v, ".")
	if len(fields) != 3 {
		return out, fmt.Errorf("invalid version")
	}
	for i, s := range fields {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return out, fmt.Errorf("invalid version")
		}
		out[i] = n
	}
	return out, nil
}
func newerVersion(a, b string) bool {
	av, _ := versionParts(a)
	bv, _ := versionParts(b)
	for i := range av {
		if av[i] != bv[i] {
			return av[i] > bv[i]
		}
	}
	return false
}

// RunMigrations is an explicit upgrade. DDL is not wrapped in a rollback transaction.
// GET_LOCK, session variables and PREPARE share one pinned MySQL connection.
func RunMigrations(db *gorm.DB, dir string) error {
	plan, err := LoadMigrationPlan(dir)
	if err != nil {
		return err
	}
	if db == nil {
		return migrationError("database_unavailable", "", 0)
	}
	return migrationConnection(db, func(ctx context.Context, c *stdsql.Conn) error {
		var locked int
		if err := c.QueryRowContext(ctx, "SELECT GET_LOCK(CONCAT('feedvideo:', DATABASE(), ':migrations'), 0)").Scan(&locked); err != nil || locked != 1 {
			return migrationError("lock_unavailable", "", 0)
		}
		defer func() {
			releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var released int
			_ = c.QueryRowContext(releaseCtx, "SELECT RELEASE_LOCK(CONCAT('feedvideo:', DATABASE(), ':migrations'))").Scan(&released)
		}()
		if err := ensureMigrationLedger(ctx, c); err != nil {
			return err
		}
		return applyMigrationPlan(ctx, c, plan, false)
	})
}

// CheckMigrations performs no DDL or bookkeeping writes. Production uses this gate.
func CheckMigrations(db *gorm.DB, dir string) error {
	plan, err := LoadMigrationPlan(dir)
	if err != nil {
		return err
	}
	if db == nil {
		return migrationError("database_unavailable", "", 0)
	}
	return migrationConnection(db, func(ctx context.Context, c *stdsql.Conn) error { return applyMigrationPlan(ctx, c, plan, true) })
}
func migrationConnection(db *gorm.DB, fn func(context.Context, *stdsql.Conn) error) error {
	pool, err := db.DB()
	if err != nil {
		return migrationError("database_unavailable", "", 0)
	}
	parent := context.Background()
	if db.Statement != nil && db.Statement.Context != nil {
		parent = db.Statement.Context
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	c, err := pool.Conn(ctx)
	if err != nil {
		return migrationError("connection_unavailable", "", 0)
	}
	defer c.Close()
	return fn(ctx, c)
}
func ensureMigrationLedger(ctx context.Context, c *stdsql.Conn) error {
	if _, err := c.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
 id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(255) NOT NULL UNIQUE,
 applied_at DATETIME NOT NULL, version BIGINT NOT NULL DEFAULT 0,
 checksum CHAR(64) NOT NULL DEFAULT '', state VARCHAR(16) NOT NULL DEFAULT 'applied',
 started_at DATETIME NULL, finished_at DATETIME NULL, error_code VARCHAR(64) NULL
 ) ENGINE=InnoDB`); err != nil {
		return migrationError("ledger_unavailable", "", 0)
	}
	// Expand the old ledger without inventing checksums for previously applied SQL.
	columns := [][2]string{{"version", "BIGINT NOT NULL DEFAULT 0"}, {"checksum", "CHAR(64) NOT NULL DEFAULT ''"}, {"state", "VARCHAR(16) NOT NULL DEFAULT 'applied'"}, {"started_at", "DATETIME NULL"}, {"finished_at", "DATETIME NULL"}, {"error_code", "VARCHAR(64) NULL"}}
	for _, column := range columns {
		var n int
		if err := c.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'schema_migrations' AND COLUMN_NAME = ?", column[0]).Scan(&n); err != nil {
			return migrationError("ledger_unavailable", "", 0)
		}
		if n == 0 {
			if _, err := c.ExecContext(ctx, "ALTER TABLE schema_migrations ADD COLUMN "+column[0]+" "+column[1]); err != nil {
				return migrationError("ledger_expand_failed", "", 0)
			}
		}
	}
	return nil
}
func applyMigrationPlan(ctx context.Context, c *stdsql.Conn, plan []Migration, check bool) error {
	// Preflight all recorded versions before application DDL, including later versions.
	pending := make([]Migration, 0, len(plan))
	for _, m := range plan {
		var checksum, state string
		err := c.QueryRowContext(ctx, "SELECT checksum, state FROM schema_migrations WHERE name = ?", m.Name).Scan(&checksum, &state)
		if err == stdsql.ErrNoRows {
			if check {
				return migrationError("schema_version_missing", m.Name, 0)
			}
			pending = append(pending, m)
			continue
		}
		if err != nil {
			return migrationError("ledger_unavailable", m.Name, 0)
		}
		if checksum == "" {
			return migrationError("legacy_schema_unverified", m.Name, 0)
		}
		if checksum != m.Checksum {
			return migrationError("checksum_mismatch", m.Name, 0)
		}
		if state != "applied" {
			return migrationError("previous_migration_incomplete", m.Name, 0)
		}
	}
	for _, m := range pending {
		if _, err := c.ExecContext(ctx, "INSERT INTO schema_migrations (name, version, checksum, state, applied_at, started_at) VALUES (?, ?, ?, 'running', UTC_TIMESTAMP(), UTC_TIMESTAMP())", m.Name, m.Version, m.Checksum); err != nil {
			return migrationError("ledger_write_failed", m.Name, 0)
		}
		for step, statement := range m.statements {
			if _, err := c.ExecContext(ctx, statement); err != nil {
				_, markErr := c.ExecContext(ctx, "UPDATE schema_migrations SET state = ?, finished_at = UTC_TIMESTAMP(), error_code = ? WHERE name = ?", "failed", fmt.Sprintf("ddl_step_%d", step+1), m.Name)
				if markErr != nil {
					return migrationError("failure_record_failed", m.Name, step+1)
				}
				return migrationError("ddl_failed", m.Name, step+1)
			}
		}
		if _, err := c.ExecContext(ctx, "UPDATE schema_migrations SET state = ?, applied_at = UTC_TIMESTAMP(), finished_at = UTC_TIMESTAMP(), error_code = NULL WHERE name = ?", "applied", m.Name); err != nil {
			return migrationError("ledger_write_failed", m.Name, len(m.statements))
		}
	}
	return nil
}
