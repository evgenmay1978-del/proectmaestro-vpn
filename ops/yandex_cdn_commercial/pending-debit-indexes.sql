-- Additive read-path indexes installed on the existing rqlite cluster.
-- They do not change orders, balances, receipts, or immutable metering history.
CREATE INDEX IF NOT EXISTS idx_commercial_applied_receipt_cover
ON idempotency_requests(scope,command_type,status,resource_id,idempotency_key,request_hash);
CREATE INDEX IF NOT EXISTS idx_commercial_outbox_receipt_cover
ON whitelist_commercial_debit_outbox(entitlement_id,receipt_key,request_hash,event_id);

-- Narrow rollback, only if these indexes cause a measured regression:
-- DROP INDEX idx_commercial_applied_receipt_cover;
-- DROP INDEX idx_commercial_outbox_receipt_cover;
