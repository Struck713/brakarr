package database

import (
	"context"
	"database/sql"
	"log"

	"github.com/Struck713/brakarr/internal/media"
	"github.com/doug-martin/goqu/v9"

	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	_ "github.com/mattn/go-sqlite3"
)

type Database struct {
	db *goqu.Database
}

func NewDatabase() *Database {
	file, err := sql.Open("sqlite3", "app.db")
	if err != nil {
		log.Fatalf("failed to open db: %v", err)
	}
	file.SetMaxOpenConns(1)

	_, err = file.Exec(`
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
	`)
	if err != nil {
		panic(err)
	}

	dialect := goqu.Dialect("sqlite3")
	db := dialect.DB(file)

	return &Database{db: db}
}

func (d *Database) Log(context context.Context, media *media.Media) error {
	_, err := d.db.Insert("files").
		Rows(media).
		Executor().
		ExecContext(context)
	if err != nil {
		return err
	}
	return nil
}

func (d *Database) Get(context context.Context, hash []byte) (*media.Media, error) {
	media := &media.Media{}
	found, err := d.db.From("files").
		Select().
		Where(goqu.Ex{"hash": hash}).
		ScanStructContext(context, media)

	if err != nil {
		return nil, err
	}

	if !found {
		return nil, nil
	}

	return media, nil
}

func (d *Database) GetJobsByStatus(context context.Context, status media.Status) ([]media.Media, error) {
	m := []media.Media{}
	err := d.db.From("files").
		Select().
		Where(goqu.Ex{"status": status}).
		ScanStructsContext(context, &m)

	if err != nil {
		return nil, err
	}

	return m, nil
}

func (d *Database) GetJobByStatus(context context.Context, status media.Status) (*media.Media, error) {
	m := media.Media{}
	found, err := d.db.From("files").
		Select().
		Where(goqu.Ex{"status": status}).
		ScanStructContext(context, &m)

	if !found {
		return nil, sql.ErrNoRows
	}

	if err != nil {
		return nil, err
	}

	return &m, nil
}

func (d *Database) UpdateStatus(context context.Context, media *media.Media, status media.Status) error {
	_, err := d.db.Update("files").
		Set(goqu.Ex{"status": status}).
		Where(goqu.Ex{"hash": media.Hash}).
		Executor().
		ExecContext(context)
	return err
}

func (d *Database) RequeueRunning(context context.Context) error {
	_, err := d.db.Update("files").
		Set(goqu.Ex{"status": media.StatusPending}).
		Where(goqu.Ex{"status": media.StatusRunning}).
		Executor().
		ExecContext(context)
	return err
}

func (d *Database) All(context context.Context) ([]media.Media, error) {
	medias := []media.Media{}
	err := d.db.From("files").
		Select().
		ScanStructsContext(context, &medias)

	if err != nil {
		return nil, err
	}

	return medias, nil
}
