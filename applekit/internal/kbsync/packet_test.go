package kbsync

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/bits"
	"os"
	"testing"
)

// Authenticate before inspecting framing; variable field values are not pinned.
func openTestPacket(hardware []byte, dsid uint64, packet []byte) ([144]byte, error) {
	var message [144]byte
	if len(hardware) != 6 || len(packet) != 196 || binary.BigEndian.Uint32(packet[:4]) != 0x00040003 {
		return message, fmt.Errorf("invalid keybag framing")
	}
	key := deriveBodyKey([6]byte(hardware), [12]byte(packet[4:16]), dsid)
	macKey := key
	for i := range macKey {
		macKey[i] ^= bodyFormat.MacMask[i]
	}
	mac := hmac.New(sha1.New, macKey[:])
	mac.Write(packet[:176])
	if !hmac.Equal(mac.Sum(nil), packet[176:]) {
		return message, fmt.Errorf("invalid keybag MAC")
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return message, err
	}
	var plain [160]byte
	cipher.NewCBCDecrypter(block, bodyFormat.Cipher.Iv[:]).CryptBlocks(plain[:], packet[16:176])
	if binary.BigEndian.Uint32(plain[:4]) != 144 || !bytes.Equal(plain[148:], make([]byte, 12)) {
		return message, fmt.Errorf("invalid encrypted length or padding")
	}
	copy(message[:], plain[4:148])
	return message, checkTestMessage(message)
}

func checkTestMessage(message [144]byte) error {
	for i, want := range [...]uint32{0x02050a00, 0, 1, 0, 1, 104} {
		if binary.BigEndian.Uint32(message[4*i:]) != want {
			return fmt.Errorf("invalid message header word %d", i)
		}
	}
	for _, field := range [...]struct {
		offset int
		value  uint32
	}{{25, 5}, {33, 1}, {42, 52}, {102, 1}, {111, 1}, {120, 4}} {
		word := binary.BigEndian.Uint32(message[field.offset:])
		width := word >> scalarPayloadBits
		if width != uint32(bits.Len32(field.value)) || word&(uint32(1)<<width-1) != field.value {
			return fmt.Errorf("invalid message count or field length at %d", field.offset)
		}
	}
	for _, field := range [...]struct {
		offset int
		value  uint32
	}{{29, 0x184be410}, {38, 0x92da105c}, {98, 0x1b94cbe1}, {107, 0xe403dd27}, {116, 0x48223fa7}, {124, 2}} {
		if binary.BigEndian.Uint32(message[field.offset:]) != field.value {
			return fmt.Errorf("invalid message framing at %d", field.offset)
		}
	}
	for i, offset := range [...]int{24, 37, 106, 115} {
		if message[offset] != [...]byte{1, 2, 1, 1}[i] {
			return fmt.Errorf("invalid message marker at %d", offset)
		}
	}
	return nil
}

func TestNativePacketCompatibility(t *testing.T) {
	raw, err := os.ReadFile("testdata/native_packets.json")
	if err != nil {
		t.Fatal(err)
	}
	var packets []struct {
		Hardware []byte
		Dsid     uint64
		Blob     []byte
	}
	if err := json.Unmarshal(raw, &packets); err != nil {
		t.Fatal(err)
	}
	if len(packets) != 20 {
		t.Fatal("incomplete native corpus")
	}
	for i, v := range packets {
		generated, err := Derive(v.Hardware, v.Dsid)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := openTestPacket(v.Hardware, v.Dsid, generated); err != nil {
			t.Fatalf("generated packet %d: %v", i, err)
		}
		message, err := openTestPacket(v.Hardware, v.Dsid, v.Blob)
		if err != nil {
			t.Fatalf("native packet %d: %v", i, err)
		}
		nonce := [12]byte(v.Blob[4:16])
		key := deriveBodyKey([6]byte(v.Hardware), nonce, v.Dsid)
		if got := sealBody(message, nonce, key); !bytes.Equal(got[:], v.Blob) {
			t.Fatalf("explicit native inputs produced a different packet %d", i)
		}
	}
}
