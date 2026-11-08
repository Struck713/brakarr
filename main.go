package main

import (
	"os"

	"github.com/Struck713/brakarr/crawler"
	"github.com/Struck713/brakarr/database"
	"github.com/Struck713/brakarr/web"
)

func main() {

	db := database.NewDatabase()
	web := web.NewWeb(db)

	crawler := crawler.NewCrawler(db, os.Getenv("CRAWL_PATH"), []string{".mkv"})
	go crawler.Crawl()

	web.Init()
}
