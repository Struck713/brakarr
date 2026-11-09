//go:build !unix

package media

import "io/fs"

// LinkCount returns the number of hard links to the file. Not supported here.
func LinkCount(fs.FileInfo) uint64 { return 1 }

// CopyOwner does nothing on this platform.
func CopyOwner(fs.FileInfo, string) {}
