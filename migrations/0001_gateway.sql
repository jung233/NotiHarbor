-- Version 1: reliable notification gateway tables. Run with a verified backup.
-- The application migration entry point is model.MigrateGateway; this file is the
-- equivalent explicit MySQL schema for operators who manage migrations externally.
CREATE TABLE IF NOT EXISTS gateway_schema_versions (
    version INT NOT NULL PRIMARY KEY,
    applied_at DATETIME(3) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS gateway_sources (
    id CHAR(36) NOT NULL PRIMARY KEY,
    owner_user_id INT NOT NULL,
    name VARCHAR(191) NOT NULL,
    description TEXT,
    allowed_channel_ids_json TEXT,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    rate_limit_per_minute INT NOT NULL DEFAULT 0,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    INDEX idx_gateway_sources_owner_user_id (owner_user_id),
    INDEX idx_gateway_sources_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS gateway_api_keys (
    id CHAR(36) NOT NULL PRIMARY KEY,
    source_id CHAR(36) NOT NULL,
    name VARCHAR(191) NOT NULL,
    prefix VARCHAR(16) NOT NULL,
    key_hash CHAR(64) NOT NULL,
    scopes_json TEXT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    expires_at DATETIME(3) NULL,
    last_used_at DATETIME(3) NULL,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    UNIQUE KEY idx_gateway_api_keys_key_hash (key_hash),
    INDEX idx_gateway_api_keys_source_id (source_id),
    INDEX idx_gateway_api_keys_prefix (prefix),
    INDEX idx_gateway_api_keys_status (status),
    INDEX idx_gateway_api_keys_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS gateway_notifications (
    id CHAR(36) NOT NULL PRIMARY KEY,
    source_id CHAR(36) NOT NULL,
    idempotency_key VARCHAR(191) NOT NULL,
    request_hash CHAR(64) NOT NULL,
    request_id VARCHAR(191),
    title VARCHAR(512),
    content LONGTEXT,
    metadata_json LONGTEXT,
    priority VARCHAR(32),
    delivery_policy VARCHAR(32) NOT NULL DEFAULT 'broadcast',
    status VARCHAR(32) NOT NULL,
    is_test BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    completed_at DATETIME(3) NULL,
    UNIQUE KEY idx_gateway_source_idempotency (source_id, idempotency_key),
    INDEX idx_gateway_notifications_source_created (source_id, created_at),
    INDEX idx_gateway_notifications_request_id (request_id),
    INDEX idx_gateway_notifications_priority (priority),
    INDEX idx_gateway_notifications_status (status),
    INDEX idx_gateway_notifications_is_test (is_test)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS gateway_deliveries (
    id CHAR(36) NOT NULL PRIMARY KEY,
    notification_id CHAR(36) NOT NULL,
    channel_id VARCHAR(191),
    channel_name VARCHAR(191),
    channel_type VARCHAR(64),
    message_type VARCHAR(64),
    message_sequence INT NOT NULL DEFAULT 0,
    metadata_json LONGTEXT,
    target_id CHAR(36),
    target TEXT,
    route_order INT NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL,
    attempt_count INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 5,
    preparation_failure_count INT NOT NULL DEFAULT 0,
    unknown_retry_count INT NOT NULL DEFAULT 0,
    retry_backoff_json VARCHAR(512),
    dispatch_generation INT NOT NULL DEFAULT 1,
    next_retry_at DATETIME(3) NULL,
    lease_owner VARCHAR(191),
    lease_token CHAR(36),
    lease_expires_at DATETIME(3) NULL,
    last_error_code VARCHAR(191),
    last_error_category VARCHAR(64),
    last_error_message TEXT,
    completed_at DATETIME(3) NULL,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    INDEX idx_gateway_deliveries_notification_id (notification_id),
    INDEX idx_gateway_delivery_route_order (notification_id, route_order),
    INDEX idx_gateway_deliveries_channel_id (channel_id),
    INDEX idx_gateway_deliveries_target_id (target_id),
    INDEX idx_gateway_deliveries_status (status),
    INDEX idx_gateway_deliveries_next_retry_at (next_retry_at),
    INDEX idx_gateway_deliveries_lease_owner (lease_owner),
    INDEX idx_gateway_deliveries_lease_expires_at (lease_expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS gateway_delivery_attempts (
    id CHAR(36) NOT NULL PRIMARY KEY,
    delivery_id CHAR(36) NOT NULL,
    attempt_number INT NOT NULL,
    attempt_kind VARCHAR(32) NOT NULL DEFAULT 'send',
    started_at DATETIME(3) NOT NULL,
    finished_at DATETIME(3) NULL,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    http_status INT,
    provider_error_code VARCHAR(191),
    error_category VARCHAR(64),
    error_message TEXT,
    provider_message_id VARCHAR(191),
    result VARCHAR(32) NOT NULL,
    next_retry_at DATETIME(3) NULL,
    created_at DATETIME(3) NOT NULL,
    UNIQUE KEY idx_gateway_attempt_number (delivery_id, attempt_number),
    INDEX idx_gateway_delivery_attempts_delivery_id (delivery_id),
    INDEX idx_gateway_delivery_attempts_result (result)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS gateway_outbox_events (
    id CHAR(36) NOT NULL PRIMARY KEY,
    delivery_id CHAR(36) NOT NULL,
    generation INT NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    available_at DATETIME(3) NOT NULL,
    lease_owner VARCHAR(191),
    lease_token CHAR(36),
    lease_expires_at DATETIME(3) NULL,
    attempt_count INT NOT NULL DEFAULT 0,
    stream_message_id VARCHAR(128),
    last_error TEXT,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    UNIQUE KEY idx_gateway_outbox_generation (delivery_id, generation),
    INDEX idx_gateway_outbox_events_delivery_id (delivery_id),
    INDEX idx_gateway_outbox_events_status (status),
    INDEX idx_gateway_outbox_events_available_at (available_at),
    INDEX idx_gateway_outbox_events_lease_expires_at (lease_expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS gateway_targets (
    id CHAR(36) NOT NULL PRIMARY KEY,
    source_id CHAR(36) NOT NULL,
    channel_id VARCHAR(191) NOT NULL,
    target_type VARCHAR(64) NOT NULL,
    external_id VARCHAR(191) NOT NULL,
    name VARCHAR(191),
    source VARCHAR(64),
    last_interaction_at DATETIME(3) NULL,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    last_send_status VARCHAR(32),
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    UNIQUE KEY idx_gateway_target_external (source_id, channel_id, target_type, external_id),
    INDEX idx_gateway_targets_source_id (source_id),
    INDEX idx_gateway_targets_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS gateway_audit_logs (
    id CHAR(36) NOT NULL PRIMARY KEY,
    actor_type VARCHAR(32) NOT NULL,
    actor_id VARCHAR(191),
    source_id CHAR(36),
    action VARCHAR(191) NOT NULL,
    resource_type VARCHAR(64) NOT NULL,
    resource_id VARCHAR(191),
    request_id VARCHAR(191),
    details_json TEXT,
    created_at DATETIME(3) NOT NULL,
    INDEX idx_gateway_audit_logs_actor_id (actor_id),
    INDEX idx_gateway_audit_logs_source_id (source_id),
    INDEX idx_gateway_audit_logs_action (action),
    INDEX idx_gateway_audit_logs_resource_id (resource_id),
    INDEX idx_gateway_audit_logs_request_id (request_id),
    INDEX idx_gateway_audit_logs_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS gateway_events (
    id CHAR(36) NOT NULL PRIMARY KEY,
    channel_id VARCHAR(191) NOT NULL,
    event_id VARCHAR(191) NOT NULL,
    event_type VARCHAR(64),
    message_id VARCHAR(191),
    message_sequence BIGINT NOT NULL DEFAULT 0,
    target_type VARCHAR(64),
    target_open_id VARCHAR(191),
    payload_hash CHAR(64),
    status VARCHAR(32) NOT NULL,
    attempt_count INT NOT NULL DEFAULT 0,
    lease_owner VARCHAR(191),
    lease_token CHAR(36),
    lease_expires_at DATETIME(3) NULL,
    last_error TEXT,
    received_at DATETIME(3) NOT NULL,
    processed_at DATETIME(3) NULL,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    UNIQUE KEY idx_gateway_event_identity (channel_id, event_id),
    INDEX idx_gateway_events_status (status),
    INDEX idx_gateway_events_lease_expires_at (lease_expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS gateway_qqbot_message_sequences (
    bot_app_id VARCHAR(191) NOT NULL,
    message_id VARCHAR(191) NOT NULL,
    target_type VARCHAR(64) NOT NULL,
    last_sequence INT NOT NULL DEFAULT 0,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    UNIQUE KEY idx_gateway_qqbot_message_sequence (bot_app_id, message_id, target_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

INSERT IGNORE INTO gateway_schema_versions (version, applied_at) VALUES (1, UTC_TIMESTAMP(3));
