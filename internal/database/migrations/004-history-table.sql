CREATE TABLE history (
    id INTEGER PRIMARY KEY,
    files_hash BLOB NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    finished_at INTEGER,
    status TEXT NOT NULL CHECK(status IN ('failed', 'skipped', 'complete')) DEFAULT 'complete',
    reason TEXT NOT NULL DEFAULT '',
    source_size INTEGER NOT NULL DEFAULT 0, -- bytes
    output_size INTEGER,                    -- bytes; NULL when there was no verified output
    vmaf REAL,                              -- NULL when the quality check did not run
    FOREIGN KEY (files_hash) REFERENCES files(hash) ON UPDATE CASCADE ON DELETE CASCADE
);

-- History replaces the per-file attempt counter.
ALTER TABLE files DROP COLUMN attempts;
