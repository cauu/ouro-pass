-- §7 Push, §8 Admin & audit.
CREATE TABLE PushJob (
    job_id               TEXT PRIMARY KEY,
    pool_id              TEXT NOT NULL,
    title                TEXT NOT NULL,
    content              TEXT NOT NULL,
    channel_type         TEXT NOT NULL,
    target_topic         TEXT,
    required_entitlement TEXT,
    target_tier          TEXT,
    status               TEXT NOT NULL,
    scheduled_at         TEXT,
    created_by           TEXT NOT NULL,
    created_at           TEXT NOT NULL
);
CREATE INDEX idx_pushjob_status ON PushJob(status);

CREATE TABLE DeliveryLog (
    delivery_id     TEXT PRIMARY KEY,
    job_id          TEXT NOT NULL,
    session_id      TEXT NOT NULL,
    channel_type    TEXT NOT NULL,
    channel_user_id TEXT NOT NULL,
    status          TEXT NOT NULL,
    retry_count     INTEGER NOT NULL DEFAULT 0,
    error_message   TEXT,
    sent_at         TEXT
);
CREATE INDEX idx_deliverylog_job ON DeliveryLog(job_id);

CREATE TABLE AdminUser (
    admin_id       TEXT PRIMARY KEY,
    pool_id        TEXT NOT NULL,
    owner_key_hash TEXT NOT NULL UNIQUE,
    role           TEXT NOT NULL,
    last_login_at  TEXT,
    created_at     TEXT NOT NULL
);

CREATE TABLE AdminSession (
    session_token TEXT PRIMARY KEY,
    admin_id      TEXT NOT NULL,
    expires_at    TEXT NOT NULL,
    ip            TEXT,
    created_at    TEXT NOT NULL
);

CREATE TABLE AuditLog (
    audit_id    TEXT PRIMARY KEY,
    actor       TEXT NOT NULL,
    action      TEXT NOT NULL,
    target      TEXT NOT NULL,
    before_hash TEXT,
    after_hash  TEXT,
    ip          TEXT,
    created_at  TEXT NOT NULL
);
CREATE INDEX idx_auditlog_created ON AuditLog(created_at);
