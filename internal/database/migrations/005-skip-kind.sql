-- Why a file was skipped, so skipped files that can be retried are found without matching text.
ALTER TABLE files ADD COLUMN skip_kind TEXT NOT NULL DEFAULT ''
    CHECK(skip_kind IN ('', 'hevc', 'quality', 'not_smaller'));
UPDATE files SET skip_kind = CASE
    WHEN error = 'already HEVC' THEN 'hevc'
    WHEN error LIKE 'quality too low%' THEN 'quality'
    WHEN error LIKE 'output not smaller%' THEN 'not_smaller'
    ELSE ''
END
WHERE status = 'skipped';
