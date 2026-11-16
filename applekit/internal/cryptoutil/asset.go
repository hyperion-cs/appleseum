package cryptoutil

import (
	"encoding/binary"
	"fmt"
	"io/fs"
)

// Assets use packed little-endian fields without Go struct padding.
func ReadAsset[T any](files fs.ReadFileFS, name string) (value T) {
	raw, err := files.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("cryptoutil: read embedded %s: %v", name, err))
	}
	if size := binary.Size(value); size < 0 || len(raw) != size {
		panic(fmt.Sprintf("cryptoutil: embedded %s has %d bytes, expected %d", name, len(raw), size))
	}
	if _, err := binary.Decode(raw, binary.LittleEndian, &value); err != nil {
		panic(fmt.Sprintf("cryptoutil: decode embedded %s: %v", name, err))
	}
	return
}
