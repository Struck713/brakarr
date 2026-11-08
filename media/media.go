package media

import (
	"fmt"

	"github.com/zelenin/go-mediainfo"
)

type Media struct {
	Path     string `json:"path"`
	Title    string `json:"title"`
	Format   string `json:"format"`
	BitDepth string `json:"bit_depth"`
	Size     string `json:"size"`
}

func NewMedia(path string) (*Media, error) {
	info, err := mediainfo.Open(path)
	if err != nil {
		return nil, fmt.Errorf("mediainfo open: %s", err)
	}
	defer info.Close()

	title := info.Parameter(mediainfo.StreamGeneral, 0, "Title")
	size := info.Parameter(mediainfo.StreamGeneral, 0, "FileSize")
	format := info.Parameter(mediainfo.StreamVideo, 0, "Format")
	bit_depth := info.Parameter(mediainfo.StreamVideo, 0, "BitDepth")
	return &Media{
		Path:     path,
		Title:    title,
		Size:     size,
		Format:   format,
		BitDepth: bit_depth,
	}, nil
}

// func Execute2() {
// 	cmd := exec.Command("sleep", "5")

// 	err := cmd.Start()
// 	if err != nil {
// 		log.Fatal(err)
// 	}
// 	fmt.Println("Application is running in the background...")

// 	// Do other work here...

// 	// Optional: Wait for it to finish later if needed
// 	err = cmd.Wait()
// 	fmt.Println("Application finished.")
// }
