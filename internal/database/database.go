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

const columns = "hash, name, path, format, bit_depth, size, duration, created_at, status, error, attempts"

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

// scan reads one media row from s.
func scan(s scanner) (*media.Media, error) {
	m := &media.Media{}
	err := s.Scan(&m.Hash, &m.Name, &m.Path, &m.Format, &m.BitDepth, &m.Size,
		&m.Duration, &m.CreatedAt, &m.Status, &m.Error, &m.Attempts)
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
		INSERT OR IGNORE INTO files (hash, name, path, format, bit_depth, size, duration, status, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Hash, m.Name, m.Path, m.Format, m.BitDepth, m.Size, m.Duration, status, m.Error)
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
		SET status = 'running', attempts = attempts + 1, error = ''
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

// Fail marks the media as failed with the given reason.
func (d *Database) Fail(ctx context.Context, hash []byte, reason string) error {
	_, err := d.db.ExecContext(ctx, `UPDATE files SET status = 'failed', error = ? WHERE hash = ?`, reason, hash)
	return err
}

// Skip marks the media as skipped with the given reason.
func (d *Database) Skip(ctx context.Context, hash []byte, reason string) error {
	_, err := d.db.ExecContext(ctx, `UPDATE files SET status = 'skipped', error = ? WHERE hash = ?`, reason, hash)
	return err
}

// Defer marks the media as deferred with the given reason and refunds the attempt.
func (d *Database) Defer(ctx context.Context, hash []byte, reason string) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE files SET status = 'deferred', error = ?, attempts = MAX(attempts - 1, 0) WHERE hash = ?`, reason, hash)
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

// Release returns a claimed item to pending and refunds the attempt.
func (d *Database) Release(ctx context.Context, hash []byte) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE files SET status = 'pending', attempts = MAX(attempts - 1, 0) WHERE hash = ?`, hash)
	return err
}

// Retry returns a failed item to pending, reporting whether it changed.
func (d *Database) Retry(ctx context.Context, hash []byte) (bool, error) {
	res, err := d.db.ExecContext(ctx,
		`UPDATE files SET status = 'pending', error = '' WHERE hash = ? AND status = 'failed'`, hash)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RequeueRunning resets items left running by a previous run back to pending.
func (d *Database) RequeueRunning(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, `UPDATE files SET status = 'pending' WHERE status = 'running'`)
	return err
}

// Replace swaps an item's hash and metadata for the transcoded file and marks it complete.
func (d *Database) Replace(ctx context.Context, oldHash []byte, next *media.Media) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM files WHERE hash = ? AND hash != ?`, next.Hash, oldHash); err != nil {
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
