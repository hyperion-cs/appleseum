//go:build linux || darwin

package library

import "golang.org/x/sys/unix"

func diskSpace(path string) (int64, int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	if stat.Files > 0 && stat.Ffree == 0 {
		return 0, int64(stat.Blocks) * int64(stat.Bsize), nil
	}
	return int64(stat.Bavail) * int64(stat.Bsize), int64(stat.Blocks) * int64(stat.Bsize), nil
}
