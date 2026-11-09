CREATE TABLE IF NOT EXISTS files (
    hash BLOB PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    path TEXT NOT NULL,
    format TEXT NOT NULL,
    bit_depth TEXT NOT NULL,
    size TEXT NOT NULL,
    duration INTEGER NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    status TEXT NOT NULL CHECK(status IN ('pending', 'running', 'failed', 'skipped', 'complete')) DEFAULT 'pending'
) WITHOUT ROWID;