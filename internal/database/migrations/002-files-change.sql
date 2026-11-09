CREATE TABLE files_new (
    hash BLOB PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    path TEXT NOT NULL,
    format TEXT NOT NULL,
    bit_depth TEXT NOT NULL,
    size INTEGER NOT NULL DEFAULT 0,
    duration INTEGER NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    status TEXT NOT NULL CHECK(status IN ('pending', 'running', 'failed', 'skipped', 'complete')) DEFAULT 'pending',
    error TEXT NOT NULL DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;
INSERT INTO files_new (hash, name, path, format, bit_depth, size, duration, created_at, status)
    SELECT hash, name, path, format, bit_depth, CAST(size AS INTEGER), duration, created_at, status FROM files;
DROP TABLE files;
ALTER TABLE files_new RENAME TO files;
CREATE INDEX idx_files_status_created ON files (status, created_at);