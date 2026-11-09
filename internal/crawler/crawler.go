package crawler

import (
	"io/fs"
	"log"
	"path/filepath"
	"slices"

	"github.com/Struck713/brakarr/internal/database"
	"github.com/Struck713/brakarr/internal/media"
	"github.com/kalafut/imohash"
)

type Crawler struct {
	db         *database.Database
	root       string
	extensions []string
}

func NewCrawler(db *database.Database, root string, extensions []string) Crawler {
	return Crawler{
		db:         db,
		root:       root,
		extensions: extensions,
	}
}

func (q *Crawler) Crawl() {
	log.Printf("starting crawler on: %s", q.root)
	err := filepath.WalkDir(q.root, q.Walk)

	if err != nil {
		log.Fatalf("could not crawl: %s", err)
	}

	log.Printf("crawl finished: %s", q.root)
}

func (q *Crawler) Walk(path string, d fs.DirEntry, err error) error {
	if err != nil {
		return err
	}

	if d.IsDir() || !slices.Contains(q.extensions, filepath.Ext(path)) {
		return nil
	}

	file_name := filepath.Base(path)
	log.Printf("generating hash: %s", file_name)

	hash, err := q.Hash(path)
	if err != nil {
		return err
	}
	log.Printf("hash complete: %s => %016x", file_name, hash)

	record, err := q.db.Get(hash)
	if err != nil {
		return err
	}

	if record == nil {
		log.Printf("new media: %s", filepath.Base(path))
		media, err := media.NewMedia(path, hash)
		if err != nil {
			return err
		}

		log.Printf("saving media: %+v", media)
		if err := q.db.Log(media); err != nil {
			return err
		}
	}

	return nil
}

func (q *Crawler) Hash(path string) ([]byte, error) {
	hash, err := imohash.SumFile(path)
	if err != nil {
		return nil, err
	}

	return hash[:], nil
}
