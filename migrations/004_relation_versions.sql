-- Expand: retain a monotonic version for each unordered relation pair.
CREATE TABLE IF NOT EXISTS user_relation_versions (
    low_id BIGINT NOT NULL,
    high_id BIGINT NOT NULL,
    version BIGINT NOT NULL DEFAULT 0,
    updated_at DATETIME(3) NOT NULL,
    PRIMARY KEY (low_id, high_id)
) ENGINE=InnoDB;
