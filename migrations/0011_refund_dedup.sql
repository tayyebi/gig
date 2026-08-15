-- A double-submitted admin "Refund order" form (a slow redirect, two
-- browser tabs) must not create two refund rows or post ledger entries
-- twice; only "failed" refunds may be retried with a fresh row. The
-- application layer also checks for an existing refund before calling the
-- provider, but the constraint is the actual safety net.
CREATE UNIQUE INDEX idx_refunds_order_not_failed ON refunds (order_id) WHERE status <> 'failed';
