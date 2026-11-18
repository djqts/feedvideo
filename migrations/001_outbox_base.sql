-- Expand-only baseline: preserve existing tables and data.
CREATE TABLE IF NOT EXISTS outbox_events (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  event_type VARCHAR(64) NOT NULL,
  event_id VARCHAR(64) NOT NULL,
  aggregate_type VARCHAR(32) NOT NULL,
  aggregate_id VARCHAR(64) NOT NULL,
  payload JSON NOT NULL,
  status VARCHAR(16) DEFAULT 'pending',
  attempts BIGINT DEFAULT 0,
  created_at DATETIME(3) NULL,
  published_at DATETIME(3) NULL,
  UNIQUE KEY idx_outbox_event_id (event_id),
  KEY idx_outbox_status (status),
  KEY idx_outbox_created_at (created_at)
) ENGINE=InnoDB;
