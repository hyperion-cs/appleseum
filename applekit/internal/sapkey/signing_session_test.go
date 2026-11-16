package sapkey

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"testing"
)

func configuratorSigningFixture(t testing.TB) (ConfiguratorRequestState, []byte) {
	t.Helper()
	var state ConfiguratorRequestState
	for i := range state.material {
		state.material[i] = byte(i + 1)
	}
	for i := range state.credential {
		state.credential[i] = byte(i*19 + 7)
	}
	for i := range state.envelopeRandom {
		state.envelopeRandom[i] = byte(i*17 + 3)
	}
	plain := make([]byte, 1_248)
	for i := range plain {
		plain[i] = byte(i*37 + 11)
	}
	for _, offset := range []int{0, 20, 40} {
		binary.BigEndian.PutUint32(plain[offset:], 16)
	}
	reply := make([]byte, 21+len(plain)+7)
	reply[0] = 2
	binary.BigEndian.PutUint32(reply[17:21], uint32(len(plain)))
	block, err := aes.NewCipher(state.material[:16])
	if err != nil {
		t.Fatal(err)
	}
	cipher.NewCBCEncrypter(block, reply[1:17]).CryptBlocks(reply[21:21+len(plain)], plain)
	copy(reply[21+len(plain):], []byte("footer!"))
	return state, reply
}

func referenceConfiguratorSignature(t testing.TB, state ConfiguratorRequestState, reply, envelope []byte, nonce [16]byte, body []byte) []byte {
	t.Helper()
	tag, err := DeriveConfiguratorTag(state, reply, nonce[:], envelope, body)
	if err != nil {
		t.Fatal(err)
	}
	signature := make([]byte, 501)
	signature[0] = 2
	copy(signature[1:17], nonce[:])
	copy(signature[17:33], tag)
	copy(signature[33:], envelope)
	return signature
}

func TestConfiguratorSignatureFingerprint(t *testing.T) {
	const want = "dbc5c248dc1b3e669fc8d8503561c81408c53014989b10490821206744dcd8ba"

	state, reply := configuratorSigningFixture(t)
	envelope, err := DeriveConfiguratorEnvelope(state, reply)
	if err != nil {
		t.Fatal(err)
	}
	signature := referenceConfiguratorSignature(t, state, reply, envelope, [16]byte{1, 2, 3, 4}, []byte("synthetic action"))
	if got := fmt.Sprintf("%x", sha256.Sum256(signature)); got != want {
		t.Fatalf("signature fingerprint %s, want %s", got, want)
	}
	if !bytes.Equal(signature[33:], envelope) {
		t.Fatal("envelope changed")
	}
}

func TestPreparedSigningSessionMatchesExistingApi(t *testing.T) {
	state, reply := configuratorSigningFixture(t)
	original := bytes.Clone(reply)
	envelope, err := DeriveConfiguratorEnvelope(state, reply)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := NewConfiguratorSession(state, reply)
	if err != nil {
		t.Fatal(err)
	}
	retained := prepared.envelope
	for i := range 32 {
		nonce := [16]byte{byte(i), 3, 4, 5}
		body := bytes.Repeat([]byte{byte(i * 17)}, i*13)
		want := referenceConfiguratorSignature(t, state, reply, envelope, nonce, body)
		if got := prepared.Sign(nonce, body); !bytes.Equal(got, want) {
			t.Fatal("prepared signature differs", i)
		}
	}
	if !bytes.Equal(reply, original) {
		t.Fatal("setup input mutated")
	}
	prepared.Close()
	prepared.Close()
	if prepared.block != nil || !bytes.Equal(retained, make([]byte, len(retained))) {
		t.Fatal("closed session retained material")
	}
}

func TestPreparedSessionsRejectMalformedSetup(t *testing.T) {
	state, valid := configuratorSigningFixture(t)
	for _, reply := range [][]byte{nil, valid[:20], valid[:25], valid[:100]} {
		if _, err := NewConfiguratorSession(state, reply); err == nil {
			t.Fatal("invalid Configurator reply accepted")
		}
	}
}

func BenchmarkConfiguratorSigning(b *testing.B) {
	state, reply := configuratorSigningFixture(b)
	prepared, err := NewConfiguratorSession(state, reply)
	if err != nil {
		b.Fatal(err)
	}
	nonce := [16]byte{1, 2, 3, 4}
	body := []byte("synthetic action")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		benchmarkSignature = prepared.Sign(nonce, body)
	}
}

var benchmarkSignature []byte
