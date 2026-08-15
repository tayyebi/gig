-- Lets a seller flag a message as an explicit "I need this from you"
-- request (TODO.md Phase 4: "seller requests for buyer information"),
-- rather than only the plain, unmarked order-message thread.
ALTER TABLE order_messages ADD COLUMN is_info_request boolean NOT NULL DEFAULT false;
