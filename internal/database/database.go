package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Struck713/brakarr/internal/media"
	_ "github.com/mattn/go-sqlite3"
)

// Database is the SQLite-backed store of media files.
type Database struct {
	db *sql.DB
}

// Open opens the database at path and applies migrations.
func Open(path string) (*Database, error) {
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Database{db: db}, nil
}

// Close closes the underlying database.
func (d *Database) Close() error { return d.db.Close() }

const columns = "hash, name, path, format, bit_depth, size, duration, created_at, status, skip_kind, error"

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

// scan reads one media row from s.
func scan(s scanner) (*media.Media, error) {
	m := &media.Media{}
	err := s.Scan(&m.Hash, &m.Name, &m.Path, &m.Format, &m.BitDepth, &m.Size,
		&m.Duration, &m.CreatedAt, &m.Status, &m.SkipKind, &m.Error)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// Insert adds a media row, reporting whether it was newly inserted.
func (d *Database) Insert(ctx context.Context, m *media.Media) (bool, error) {
	status := m.Status
	if status == "" {
		status = media.StatusPending
	}
	res, err := d.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO files (hash, name, path, format, bit_depth, size, duration, status, skip_kind, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Hash, m.Name, m.Path, m.Format, m.BitDepth, m.Size, m.Duration, status, m.SkipKind, m.Error)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Get returns the media with the given hash, or nil if unknown.
func (d *Database) Get(ctx context.Context, hash []byte) (*media.Media, error) {
	m, err := scan(d.db.QueryRowContext(ctx, `SELECT `+columns+` FROM files WHERE hash = ?`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

// ClaimNext marks the next pending item as running and returns it, or nil if none.
func (d *Database) ClaimNext(ctx context.Context) (*media.Media, error) {
	m, err := scan(d.db.QueryRowContext(ctx, `
		UPDATE files
		SET status = 'running', skip_kind = '', error = ''
		WHERE hash = (SELECT hash FROM files WHERE status = 'pending' ORDER BY created_at, name LIMIT 1)
		RETURNING `+columns))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

// SetStatus updates the status of the media with the given hash.
func (d *Database) SetStatus(ctx context.Context, hash []byte, status media.Status) error {
	_, err := d.db.ExecContext(ctx, `UPDATE files SET status = ? WHERE hash = ?`, status, hash)
	return err
}

// recordHistory appends a finished job to the history table, taking the source size from the
// file's current row, so it must run before that row is updated with the output's metadata.
// Status must be failed, skipped or complete; a nil outputSize or vmaf is stored as NULL.
func recordHistory(ctx context.Context, tx *sql.Tx, hash []byte, status media.Status, reason string,
	outputSize *int64, vmaf *float64) error {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO history (files_hash, status, reason, source_size, output_size, vmaf, finished_at)
		SELECT hash, ?, ?, size, ?, ?, unixepoch() FROM files WHERE hash = ?`,
		status, reason, outputSize, vmaf, hash)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return fmt.Errorf("record history: no file with hash %x", hash)
	}
	return nil
}

// Fail marks the media as failed with the given reason and records it in the history.
func (d *Database) Fail(ctx context.Context, hash []byte, reason string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `UPDATE files SET status = 'failed', error = ? WHERE hash = ?`, reason, hash); err != nil {
		return err
	}
	if err := recordHistory(ctx, tx, hash, media.StatusFailed, reason, nil, nil); err != nil {
		return err
	}
	return tx.Commit()
}

// Skip marks the media as skipped after a transcode whose output was discarded, and records
// the output's size and VMAF score (nil if not measured) in the history.
func (d *Database) Skip(ctx context.Context, hash []byte, kind media.SkipKind, reason string, outputSize int64, vmaf *float64) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `UPDATE files SET status = 'skipped', skip_kind = ?, error = ? WHERE hash = ?`,
		kind, reason, hash); err != nil {
		return err
	}
	if err := recordHistory(ctx, tx, hash, media.StatusSkipped, reason, &outputSize, vmaf); err != nil {
		return err
	}
	return tx.Commit()
}

// Defer marks the media as deferred with the given reason.
func (d *Database) Defer(ctx context.Context, hash []byte, reason string) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE files SET status = 'deferred', error = ? WHERE hash = ?`, reason, hash)
	return err
}

// Undefer returns a deferred item to pending, reporting whether it changed.
func (d *Database) Undefer(ctx context.Context, hash []byte) (bool, error) {
	res, err := d.db.ExecContext(ctx,
		`UPDATE files SET status = 'pending', error = '' WHERE hash = ? AND status = 'deferred'`, hash)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Relocate records a file's new path. A failed job gets one more try, since
// the move is the most likely cause of the failure.
func (d *Database) Relocate(ctx context.Context, hash []byte, path, name string) error {
	_, err := d.db.ExecContext(ctx, `
		UPDATE files
		SET path = ?, name = ?,
		    error = CASE WHEN status = 'failed' THEN '' ELSE error END,
		    status = CASE WHEN status = 'failed' THEN 'pending' ELSE status END
		WHERE hash = ?`, path, name, hash)
	return err
}

// Release returns a claimed item to pending.
func (d *Database) Release(ctx context.Context, hash []byte) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE files SET status = 'pending' WHERE hash = ?`, hash)
	return err
}

// requeueable matches files that may go back to the queue: failed ones, and ones skipped after
// a transcode that other settings might make worth keeping. Already-HEVC files never match.
// Keep in sync with media.Requeueable.
const requeueable = `(status = 'failed' OR (status = 'skipped' AND skip_kind IN ('quality', 'not_smaller')))
	AND lower(format) <> 'hevc'`

// Requeue returns a failed or retryable skipped item to pending, reporting whether it changed.
func (d *Database) Requeue(ctx context.Context, hash []byte) (bool, error) {
	res, err := d.db.ExecContext(ctx, `
		UPDATE files SET status = 'pending', skip_kind = '', error = ''
		WHERE hash = ? AND `+requeueable, hash)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// passingQualitySkips selects files skipped for low quality whose last transcode scored at
// least the bound minimum VMAF. A minimum of 0 means the check is off, so all of them pass.
const passingQualitySkips = `
	SELECT f.hash FROM files f
	JOIN history h ON h.id = (SELECT MAX(id) FROM history WHERE files_hash = f.hash)
	WHERE f.status = 'skipped' AND f.skip_kind = 'quality' AND lower(f.format) <> 'hevc'
	  AND h.status = 'skipped' AND (?1 <= 0 OR h.vmaf >= ?1)`

// CountPassingQualitySkips counts the files skipped for low quality that would pass minVMAF.
func (d *Database) CountPassingQualitySkips(ctx context.Context, minVMAF float64) (int, error) {
	var n int
	err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+passingQualitySkips+`)`, minVMAF).Scan(&n)
	return n, err
}

// RequeuePassingQualitySkips returns the files skipped for low quality that would pass minVMAF
// to the queue, reporting how many were requeued.
func (d *Database) RequeuePassingQualitySkips(ctx context.Context, minVMAF float64) (int, error) {
	res, err := d.db.ExecContext(ctx, `
		UPDATE files SET status = 'pending', skip_kind = '', error = ''
		WHERE hash IN (`+passingQualitySkips+`)`, minVMAF)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// RequeueRunning resets items left running by a previous run back to pending.
func (d *Database) RequeueRunning(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, `UPDATE files SET status = 'pending' WHERE status = 'running'`)
	return err
}

// Replace swaps an item's hash and metadata for the transcoded file, marks it complete
// and records it in the history with the output's VMAF score (nil if not measured).
func (d *Database) Replace(ctx context.Context, oldHash []byte, next *media.Media, vmaf *float64) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM files WHERE hash = ? AND hash != ?`, next.Hash, oldHash); err != nil {
		return err
	}
	// Recorded before the update so source_size is the original's; the update's
	// ON UPDATE CASCADE then moves the row to the new hash.
	if err := recordHistory(ctx, tx, oldHash, media.StatusComplete, "", &next.Size, vmaf); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE files
		SET hash = ?, format = ?, bit_depth = ?, size = ?, duration = ?, status = 'complete', error = ''
		WHERE hash = ?`,
		next.Hash, next.Format, next.BitDepth, next.Size, next.Duration, oldHash)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Count returns the total number of media rows.
func (d *Database) Count(ctx context.Context) (int, error) {
	var n int
	err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM files`).Scan(&n)
	return n, err
}

// List returns a page of media rows.
func (d *Database) List(ctx context.Context, limit, offset int) ([]media.Media, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+columns+` FROM files ORDER BY created_at DESC, name LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []media.Media{}
	for rows.Next() {
		m, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// CountHistory returns the total number of history rows.
func (d *Database) CountHistory(ctx context.Context) (int, error) {
	var n int
	err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM history`).Scan(&n)
	return n, err
}

// ListHistory returns a page of history rows, newest first.
func (d *Database) ListHistory(ctx context.Context, limit, offset int) ([]media.History, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT h.id, h.files_hash, f.name, f.path, h.status, h.reason,
		       h.source_size, h.output_size, h.vmaf, h.created_at, h.finished_at
		FROM history h
		JOIN files f ON f.hash = h.files_hash
		ORDER BY h.id DESC
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []media.History{}
	for rows.Next() {
		var h media.History
		err := rows.Scan(&h.ID, &h.Hash, &h.Name, &h.Path, &h.Status, &h.Reason,
			&h.SourceSize, &h.OutputSize, &h.VMAF, &h.CreatedAt, &h.FinishedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
