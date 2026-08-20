-- Idempotency. INV-6: every mutating API call carries a client_request_id and
-- is idempotent on it.
--
-- The failure this prevents is concrete. A cashier taps "Bayar", the WiFi
-- stutters, the browser retries, and the shop has rung the same sale twice —
-- two sets of FIFO consumptions, two omzet rows, stock short by a whole cart.
-- The retry is correct behaviour on the client's part; the server has to be the
-- one that makes it safe.

-- +goose Up

CREATE TABLE request_log (
    client_request_id TEXT    NOT NULL PRIMARY KEY CHECK (length(client_request_id) = 36
                                                          AND client_request_id = lower(client_request_id)),
    user_id           TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    method            TEXT    NOT NULL,
    path              TEXT    NOT NULL,
    -- SHA-256 of the request body. The same id arriving with a different body
    -- is a client bug, and answering it with the first request's result would
    -- silently discard the second operation. It is refused instead.
    request_hash      TEXT    NOT NULL,
    -- 0 means in flight: claimed, not yet answered. A concurrent duplicate sees
    -- this and waits rather than executing a second time.
    status_code       INTEGER NOT NULL DEFAULT 0,
    response_body     TEXT    NOT NULL DEFAULT '',
    created_at        INTEGER NOT NULL,
    completed_at      INTEGER
) STRICT;

CREATE INDEX idx_request_log_created ON request_log (created_at);
CREATE INDEX idx_request_log_in_flight ON request_log (status_code) WHERE status_code = 0;

-- +goose Down
DROP INDEX idx_request_log_in_flight;
DROP INDEX idx_request_log_created;
DROP TABLE request_log;
