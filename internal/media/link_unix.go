//go:build unix

package media

import (
	"io/fs"
	"log/slog"
	"os"
	"syscall"
)

// LinkCount returns the number of hard links to the file.
func LinkCount(info fs.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink)
	}
	return 1
}

// CopyOwner gives path the same owner as info. Failure only matters if the
// replacement ends up unreadable, so it is logged rather than returned.
func CopyOwner(info fs.FileInfo, path string) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		if err := os.Chown(path, int(st.Uid), int(st.Gid)); err != nil {
			slog.Warn("could not preserve file owner", "path", path, "error", err)
		}
	}
}
