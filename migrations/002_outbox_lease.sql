-- Outbox 租约字段（幂等：列已存在则跳过）。
-- 由 shared-db/sql.RunMigrations 在 AutoMigrate 之前执行，与 struct 字段保持一致。
SET @col := (SELECT COUNT(*) FROM information_schema.COLUMNS
             WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'outbox_events' AND COLUMN_NAME = 'claimed_at');
SET @sql := IF(@col = 0, 'ALTER TABLE outbox_events ADD COLUMN claimed_at DATETIME NULL', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col := (SELECT COUNT(*) FROM information_schema.COLUMNS
             WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'outbox_events' AND COLUMN_NAME = 'claim_owner');
SET @sql := IF(@col = 0, 'ALTER TABLE outbox_events ADD COLUMN claim_owner VARCHAR(128) NULL', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col := (SELECT COUNT(*) FROM information_schema.COLUMNS
             WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'outbox_events' AND COLUMN_NAME = 'lease_until');
SET @sql := IF(@col = 0, 'ALTER TABLE outbox_events ADD COLUMN lease_until DATETIME NULL', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
