package database

import (
	"database/sql"
	"log"

	"github.com/Struck713/brakarr/media"
	"github.com/doug-martin/goqu/v9"

	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	_ "github.com/mattn/go-sqlite3"
)

const BUCKET_NAME = "files"

type Database struct {
	db *goqu.Database
}

func NewDatabase() *Database {
	file, err := sql.Open("sqlite3", "app.db")
	if err != nil {
		log.Fatalf("failed to open db: %v", err)
	}

	_, _ = file.Exec(`CREATE TABLE files (id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT, path TEXT, format TEXT, bitdepth TEXT, size TEXT);`)
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

func (d *Database) Get(path string) (*media.Media, error) {
	media := &media.Media{}
	found, err := d.db.From("files").
		Select().
		Where(goqu.Ex{"path": path}).
		ScanStruct(media)

	if err != nil {
		return nil, err
	}

	if !found {
		return nil, nil
	}

	return media, nil
}

func (d *Database) All() ([]media.Media, error) {
	medias := []media.Media{}
	err := d.db.From("files").Select().ScanStructs(&medias)

	if err != nil {
		return nil, err
	}

	return medias, nil
}
