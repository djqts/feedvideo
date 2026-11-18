-- Outbox lease 回滚（手动执行；运行前确认无 relay 正在使用租约字段）。
ALTER TABLE outbox_events
  DROP COLUMN claimed_at,
  DROP COLUMN claim_owner,
  DROP COLUMN lease_until;
