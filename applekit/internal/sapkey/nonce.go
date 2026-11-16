package sapkey

import (
	"crypto/rand"
	"encoding/binary"

	"github.com/hyperion-cs/appleseum/applekit/internal/cryptoutil"
	"github.com/hyperion-cs/appleseum/applekit/internal/mt19937"
)

const (
	nonceStateSize = 2_496
)

var nonceMasks = cryptoutil.ReadAsset[[16]byte](formatAssets, "assets/nonce.mask")

// NewNonce generates fresh entropy with the native nonzero encoding.
func NewNonce() ([16]byte, error) {
	var state [nonceStateSize]byte
	if _, err := rand.Read(state[:]); err != nil {
		return [16]byte{}, err
	}
	return nonceFromState(state[:]), nil
}

// nonceFromState runs the MT19937 KDF over the little-endian session state,
// zero-padded or truncated to 624 words, starting with word 465.
func nonceFromState(state []byte) (nonce [16]byte) {
	var padded [nonceStateSize]byte
	copy(padded[:], state)
	g := mt19937.Generator{Index: 465}
	for i := range g.State {
		g.State[i] = binary.LittleEndian.Uint32(padded[4*i:])
	}
	g.FillNonzero(nonce[:], nonceMasks[:])
	return nonce
}
