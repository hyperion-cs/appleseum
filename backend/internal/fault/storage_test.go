package fault

import (
	"fmt"
	"os"
	"syscall"
	"testing"
)

type sqliteError int

func (e sqliteError) Error() string { return "sqlite failure" }
func (e sqliteError) Code() int     { return int(e) }

func TestStorageCapacityErrorsKeepTheirCause(t *testing.T) {
	for _, err := range []error{syscall.ENOSPC, syscall.EDQUOT, &os.PathError{Op: "write", Path: "file", Err: syscall.ENOSPC}, sqliteError(13), fmt.Errorf("save: %w: %w", StorageFailed, syscall.ENOSPC)} {
		if code := Of(err); code != StorageFull {
			t.Fatalf("%v: %s", err, code)
		}
	}
	if code := Of(syscall.EACCES); code != StorageFailed {
		t.Fatal(code)
	}
}
