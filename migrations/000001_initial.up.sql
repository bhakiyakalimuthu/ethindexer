BEGIN;

CREATE TABLE blocks (
    number                  bigint PRIMARY KEY CHECK (number >= 0),
    hash                    bytea NOT NULL UNIQUE CHECK (octet_length(hash) = 32),
    parent_hash             bytea NOT NULL CHECK (octet_length(parent_hash) = 32),
    block_timestamp         bigint NOT NULL CHECK (block_timestamp >= 0),
    gas_limit               bigint NOT NULL CHECK (gas_limit >= 0),
    gas_used                bigint NOT NULL CHECK (gas_used >= 0),
    base_fee_per_gas_wei    numeric(78, 0) CHECK (base_fee_per_gas_wei >= 0),
    transaction_count       integer NOT NULL CHECK (transaction_count >= 0),
    indexed_at              timestamptz NOT NULL
);

CREATE TABLE transactions (
    hash                         bytea PRIMARY KEY CHECK (octet_length(hash) = 32),
    block_number                 bigint NOT NULL REFERENCES blocks(number) ON DELETE CASCADE,
    transaction_index            integer NOT NULL CHECK (transaction_index >= 0),
    transaction_type             smallint NOT NULL CHECK (transaction_type BETWEEN 0 AND 255),
    nonce                        bigint NOT NULL CHECK (nonce >= 0),
    sender_address               bytea NOT NULL CHECK (octet_length(sender_address) = 20),
    recipient_address            bytea CHECK (recipient_address IS NULL OR octet_length(recipient_address) = 20),
    value_wei                    numeric(78, 0) NOT NULL CHECK (value_wei >= 0),
    gas_limit                    bigint NOT NULL CHECK (gas_limit >= 0),
    gas_price_wei                numeric(78, 0) CHECK (gas_price_wei >= 0),
    max_fee_per_gas_wei          numeric(78, 0) CHECK (max_fee_per_gas_wei >= 0),
    max_priority_fee_per_gas_wei numeric(78, 0) CHECK (max_priority_fee_per_gas_wei >= 0),
    input                        bytea NOT NULL,
    UNIQUE (block_number, transaction_index)
);

CREATE TABLE event_logs (
    block_number       bigint NOT NULL REFERENCES blocks(number) ON DELETE CASCADE,
    transaction_hash   bytea NOT NULL REFERENCES transactions(hash) ON DELETE CASCADE,
    transaction_index  integer NOT NULL CHECK (transaction_index >= 0),
    log_index          integer NOT NULL CHECK (log_index >= 0),
    emitter_address    bytea NOT NULL CHECK (octet_length(emitter_address) = 20),
    topics             bytea[] NOT NULL CHECK (cardinality(topics) BETWEEN 0 AND 4),
    data               bytea NOT NULL,
    PRIMARY KEY (block_number, log_index)
);

CREATE INDEX event_logs_emitter_position_idx
    ON event_logs (emitter_address, block_number DESC, log_index DESC);

CREATE INDEX event_logs_transaction_position_idx
    ON event_logs (transaction_hash, log_index);

CREATE TABLE sync_state (
    chain_id                 bigint PRIMARY KEY CHECK (chain_id > 0),
    last_indexed_number      bigint CHECK (last_indexed_number >= 0),
    last_indexed_hash        bytea CHECK (last_indexed_hash IS NULL OR octet_length(last_indexed_hash) = 32),
    last_successful_sync_at  timestamptz,
    CHECK ((last_indexed_number IS NULL) = (last_indexed_hash IS NULL))
);

COMMENT ON COLUMN event_logs.emitter_address IS
    'Contract address that emitted the raw EVM log; addresses encoded in topics are not interpreted.';

COMMIT;
