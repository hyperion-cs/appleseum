//go:build !windows

package fault

func nativeStorageFull(error) bool { return false }
