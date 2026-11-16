// Package kbsync derives the 196-byte hardware/DSID keybag.
package kbsync

import (
	"embed"
	"encoding/binary"
	"fmt"
)

//go:embed assets
var formatAssets embed.FS

// Accept six raw bytes or a 24-byte block with a little-endian length prefix of six.
func normalizeHw(hw []byte) ([6]byte, error) {
	switch len(hw) {
	case 6:
		return [6]byte(hw), nil
	case 24:
		n := binary.LittleEndian.Uint32(hw)
		if n != 6 {
			return [6]byte{}, fmt.Errorf("kbsync: serialized hardware length must be 6, got %d", n)
		}
		return [6]byte(hw[4:10]), nil
	default:
		return [6]byte{}, fmt.Errorf("kbsync: hardware id must be 6 raw bytes or a 24-byte serialized block, got %d", len(hw))
	}
}

// Derive computes the 196-byte kbsync blob for the given hardware id and DSID.
func Derive(hw []byte, dsid uint64) ([]byte, error) {
	h, err := normalizeHw(hw)
	if err != nil {
		return nil, err
	}
	message, nonce := bodyInputs()
	key := deriveBodyKey(h, nonce, dsid)
	blob := sealBody(message, nonce, key)
	return blob[:], nil
}
