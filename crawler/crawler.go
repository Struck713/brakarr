package crawler

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"

	"github.com/Struck713/brakarr/database"
	"github.com/Struck713/brakarr/media"
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
	err := filepath.WalkDir(q.root, q.walk)

	if err != nil {
		fmt.Printf("Error walking the path: %v\n", err)
	}
}

func (q *Crawler) walk(path string, d fs.DirEntry, err error) error {
	if err != nil {
		return err
	}

	if d.IsDir() || !slices.Contains(q.extensions, filepath.Ext(path)) {
		return nil
	}

	record, err := q.db.Get(path)
	if record == nil {
		media, err := media.NewMedia(path)
		if err != nil {
			return err
		}

		if err := q.db.Log(media); err != nil {
			return err
		}
	}

	return nil
}
