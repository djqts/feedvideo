-- Expand the deterministic event identity and add lease fencing metadata.
ALTER TABLE outbox_events MODIFY COLUMN event_id VARCHAR(128) NOT NULL;

SET @col := (SELECT COUNT(*) FROM information_schema.COLUMNS
             WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'outbox_events' AND COLUMN_NAME = 'payload_hash');
SET @sql := IF(@col = 0, 'ALTER TABLE outbox_events ADD COLUMN payload_hash VARCHAR(64) NULL', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col := (SELECT COUNT(*) FROM information_schema.COLUMNS
             WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'outbox_events' AND COLUMN_NAME = 'claim_token');
SET @sql := IF(@col = 0, 'ALTER TABLE outbox_events ADD COLUMN claim_token BIGINT UNSIGNED NOT NULL DEFAULT 0', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Preserve millisecond precision for the database-clock lease CAS.
ALTER TABLE outbox_events MODIFY COLUMN claimed_at DATETIME(3) NULL,
                          MODIFY COLUMN lease_until DATETIME(3) NULL;
