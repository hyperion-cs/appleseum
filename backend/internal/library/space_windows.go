package library

import "golang.org/x/sys/windows"

func diskSpace(path string) (int64, int64, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var free, total, unused uint64
	err = windows.GetDiskFreeSpaceEx(name, &free, &total, &unused)
	return int64(free), int64(total), err
}
