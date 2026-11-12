CREATE TABLE client_downloads (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    edition_id TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
    cache_epoch TEXT NOT NULL,
    request_key TEXT NOT NULL,
    content_revision INTEGER NOT NULL CHECK(content_revision>0),
    source_identity TEXT NOT NULL,
    length INTEGER NOT NULL CHECK(length>=0),
    state TEXT NOT NULL CHECK(state IN('preparing','ready','failed','abandoned')),
    sha256 TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    UNIQUE(account_id,cache_epoch,request_key),
    CHECK((state='ready' AND length(sha256)=64) OR (state<>'ready' AND sha256=''))
) STRICT;
CREATE INDEX client_download_queue ON client_downloads(cache_epoch,state,created_at,id);
CREATE INDEX client_download_digest ON client_downloads(account_id,cache_epoch,edition_id,content_revision,source_identity,state);

CREATE TABLE client_offline_manifests (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    edition_id TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
    cache_epoch TEXT NOT NULL,
    content_revision INTEGER NOT NULL CHECK(content_revision>0),
    parsing_revision INTEGER NOT NULL CHECK(parsing_revision>0),
    structure_digest TEXT NOT NULL CHECK(length(structure_digest)=64),
    page_count INTEGER NOT NULL CHECK(page_count>0),
    directory_version TEXT NOT NULL,
    representation_version TEXT NOT NULL,
    UNIQUE(account_id,edition_id,cache_epoch,structure_digest)
) STRICT;

CREATE TABLE client_offline_receipts (
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    cache_epoch TEXT NOT NULL,
    operation_id TEXT NOT NULL,
    client_instance_id TEXT NOT NULL,
    object_kind TEXT NOT NULL CHECK(object_kind IN('edition','tag')),
    object_id TEXT NOT NULL,
    state_kind TEXT NOT NULL CHECK(state_kind IN('reading','edition_state','tag_follow')),
    previous_operation_id TEXT,
    request_digest TEXT NOT NULL CHECK(length(request_digest)=64),
    session_key TEXT,
    received_at INTEGER NOT NULL,
    result_json TEXT NOT NULL CHECK(json_valid(result_json)),
    PRIMARY KEY(account_id,cache_epoch,operation_id),
    FOREIGN KEY(account_id,cache_epoch,previous_operation_id)
        REFERENCES client_offline_receipts(account_id,cache_epoch,operation_id)
) STRICT;
CREATE INDEX client_offline_receipt_session ON client_offline_receipts(account_id,cache_epoch,session_key) WHERE session_key IS NOT NULL;
