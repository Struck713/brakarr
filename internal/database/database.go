package database

import (
	"database/sql"
	"errors"
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

func (d *Database) Log(media *media.Media) error {
	_, err := d.db.Insert("files").Rows(media).Executor().Exec()
	if err != nil {
		return err
	}
	return nil
}

func (d *Database) Get(hash []byte) (*media.Media, error) {
	media := &media.Media{}
	found, err := d.db.From("files").
		Select().
		Where(goqu.Ex{"hash": hash}).
		ScanStruct(media)

	if err != nil {
		return nil, err
	}

	if !found {
		return nil, nil
	}

	return media, nil
}

func (d *Database) GetNextNotProcessed() (*media.Media, error) {
	m := &media.Media{}
	found, err := d.db.From("files").
		Select().
		Where(goqu.Ex{"status": media.StatusPending}).
		ScanStruct(m)

	if err != nil {
		return nil, err
	}

	if !found {
		return nil, errors.New("there was no next processed")
	}

	return m, nil
}

func (d *Database) Process(media *media.Media) error {
	_, err := d.db.Update("files").
		Set(goqu.Ex{"processed": true}).
		Where(goqu.Ex{"hash": media.Hash}).
		Executor().
		Exec()
	return err
}

func (d *Database) All() ([]media.Media, error) {
	medias := []media.Media{}
	err := d.db.From("files").Select().ScanStructs(&medias)

	if err != nil {
		return nil, err
	}

	return medias, nil
}
