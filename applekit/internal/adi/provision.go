package adi

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/cryptoutil"
	"github.com/hyperion-cs/appleseum/applekit/internal/mt19937"
)

const (
	provisioningSpimSize    = 347
	provisioningPtmSize     = 503
	provisioningPtmOverhead = 103 // Framing and payload fields outside the record key.
	provisioningFooter      = 0xed9281d8
)

var (
	errProvisioning       = errors.New("adi: provisioning failed")
	errProvisioningClosed = errors.New("adi: provisioning session is closed or superseded")
)

// The server must echo nonce before its key material is accepted; a zero session is closed.
type provisioningSession struct {
	environmentId int64
	unixSecond    int64
	contextKey    [16]byte
	nonce         [32]byte
	mid           [60]byte
	stream        [32]byte
	valid         bool
}

type provisioningExchange struct {
	mu      sync.Mutex
	cpim    []byte
	session provisioningSession
}

func (s *provisioningExchange) ClientData() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Clone(s.cpim)
}

func (s *provisioningExchange) Abort() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.close()
}

func (s *provisioningExchange) close() {
	clear(s.cpim)
	s.cpim = nil
	s.session = provisioningSession{}
}

// Consume the exchange even on failure. Commit shares the lock so Abort cannot overtake it.
func (s *provisioningExchange) finish(ptm, transportKey []byte, commit func(*otpRecord) error) (*otpRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.session.valid {
		return nil, errProvisioningClosed
	}
	defer s.close()
	if len(transportKey) != 16 {
		return nil, errors.New("adi: invalid provisioning transport key")
	}
	plain, err := decodeProvisioningEnd(s.session, ptm, [16]byte(transportKey))
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	keySize := len(ptm) - provisioningPtmOverhead
	keyData := plain[4 : 4+keySize]
	raw := s.session.encodeRecord(keyData)
	record, err := newOtpRecord(raw, s.session.environmentId, s.session.mid[:], keyData, s.session.stream[:])
	if err != nil {
		return nil, err
	}
	if commit != nil {
		if err := commit(record); err != nil {
			return nil, err
		}
	}
	return record, nil
}

func executeProvisioningStart(environmentId int64, spim []byte, hostUuid [16]byte, at time.Time) (cpim []byte, session provisioningSession, err error) {
	if hostUuid == [16]byte{} || !otpTime(at) {
		return nil, session, fmt.Errorf("%w: invalid installation UUID or time", errProvisioning)
	}
	if len(spim) != provisioningSpimSize {
		return nil, session, fmt.Errorf("%w: SPIM length %d", errProvisioning, len(spim))
	}
	if binary.BigEndian.Uint32(spim) != 4 || binary.BigEndian.Uint32(spim[4:]) != 240 ||
		binary.BigEndian.Uint32(spim[264:]) != 52 ||
		binary.BigEndian.Uint32(spim[320:]) != provisioningFooter || !provisioningChecksumMatches(spim[4:324], spim[324:344]) {
		return nil, session, fmt.Errorf("%w: invalid SPIM", errProvisioning)
	}
	session = provisioningSession{
		environmentId: environmentId,
		unixSecond:    at.Unix(),
		contextKey:    deriveProvisioningContextKey([240]byte(spim[8:248])),
	}
	rng := provisioningRandomStart

	fill := func(dst []byte) { rng.FillNonzero(dst, provisioningRandomMask) }

	fill(session.stream[:])
	session.stream = encryptProvisioningStream(session.stream)
	fill(session.nonce[:])
	mid := provisioningMachineId(hostUuid)
	copy(session.mid[:20], mid[:])
	fill(session.mid[20:40])
	fill(session.mid[40:60])
	rng.Next() // Native local session number; it never goes on the wire.
	var plain [160]byte
	binary.BigEndian.PutUint32(plain[:], 32)
	copy(plain[4:36], session.stream[:])
	binary.BigEndian.PutUint32(plain[36:], 32)
	copy(plain[40:72], session.nonce[:])
	copy(plain[72:132], session.mid[:])
	binary.BigEndian.PutUint64(plain[132:], uint64(environmentId))
	binary.BigEndian.PutUint32(plain[140:], uint32(at.Unix()))
	binary.BigEndian.PutUint32(plain[144:], 1)
	fill(plain[148:])
	var iv, associated [16]byte
	fill(iv[:])
	provisioningNonceCipher.Decrypt(associated[:], spim[248:264])
	plainSize, version := len(plain), uint32(5)
	if environmentId == -1 {
		plainSize, version = 144, 4
	}
	encrypted := encryptProvisioningContext(session.contextKey, iv, associated, plain[:plainSize])
	clear(plain[:])
	clear(associated[:])
	cpim = make([]byte, plainSize+116)
	binary.BigEndian.PutUint32(cpim, version)
	copy(cpim[4:20], iv[:])
	binary.BigEndian.PutUint32(cpim[20:], uint32(plainSize))
	copy(cpim[24:24+len(encrypted)], encrypted)
	clear(encrypted)
	binary.BigEndian.PutUint32(cpim[56+plainSize:], 52)
	copy(cpim[60+plainSize:], spim[268:324])
	session.valid = true
	return cpim, session, nil
}

// The caller must clear the decrypted PTM payload.
func decodeProvisioningEnd(session provisioningSession, ptm []byte, trustKey [16]byte) ([]byte, error) {
	if len(ptm) != provisioningPtmSize && !(session.environmentId == -1 && len(ptm) == 375) {
		return nil, fmt.Errorf("%w: PTM length %d", errProvisioning, len(ptm))
	}
	keySize := len(ptm) - provisioningPtmOverhead
	cipherSize := keySize + 48
	if !session.valid || binary.BigEndian.Uint32(ptm) != 4 || binary.BigEndian.Uint32(ptm[20:]) != uint32(cipherSize) ||
		binary.BigEndian.Uint32(ptm[24+cipherSize:]) != provisioningFooter ||
		!provisioningChecksumMatches(ptm[4:28+cipherSize], ptm[28+cipherSize:48+cipherSize]) {
		return nil, fmt.Errorf("%w: invalid PTM", errProvisioning)
	}
	plain := decryptProvisioningContext(session.contextKey, [16]byte(ptm[4:20]), trustKey, ptm[24:24+cipherSize])
	if binary.BigEndian.Uint32(plain) != uint32(keySize) || binary.BigEndian.Uint32(plain[4+keySize:]) != 32 ||
		!bytes.Equal(plain[8+keySize:40+keySize], session.nonce[:]) {
		clear(plain)
		return nil, fmt.Errorf("%w: invalid PTM payload", errProvisioning)
	}
	serverTime := int64(binary.BigEndian.Uint32(plain[40+keySize:]))
	if serverTime < session.unixSecond-90 || serverTime > session.unixSecond+90 {
		clear(plain)
		return nil, fmt.Errorf("%w: PTM time outside allowed window", errProvisioning)
	}
	return plain, nil
}

func (session provisioningSession) encodeRecord(keyData []byte) []byte {
	var accountId, routing [8]byte
	binary.BigEndian.PutUint64(accountId[:], uint64(session.environmentId))
	record := make([]byte, 12, len(keyData)+softwareRecordOverhead)
	binary.BigEndian.PutUint32(record, 1)
	binary.BigEndian.PutUint32(record[4:], accountRecordTag)
	for _, field := range [...]struct {
		tag  uint32
		data []byte
	}{
		{accountIdTag, accountId[:]},
		{machineIdTag, session.mid[:]},
		{keyDataTag, keyData},
		{streamDataTag, session.stream[:]},
		{routingTag, routing[:]},
	} {
		record = binary.BigEndian.AppendUint32(record, field.tag)
		record = binary.BigEndian.AppendUint32(record, uint32(len(field.data)))
		record = append(record, field.data...)
	}
	binary.BigEndian.PutUint32(record[8:], uint32(len(record)-12))
	return record
}

func provisioningChecksumMatches(message, expected []byte) bool {
	checksum := syncChecksum(message)
	return bytes.Equal(checksum[:], expected)
}

var contextFormat = cryptoutil.ReadAsset[struct {
	encryptedKeyParameters
	ImportKey [16]byte
	Salt      [24]byte
}](formatAssets, "assets/context.bin")
var provisioningContextFieldCipher = cryptoutil.NewCbc(contextFormat.Cipher)
var provisioningContextImportCipher = cryptoutil.Aes128(contextFormat.ImportKey)
var provisioningNonceCipher = cryptoutil.Aes128(cryptoutil.ReadAsset[[16]byte](formatAssets, "assets/provisioning-nonce.aes128.key"))

func deriveProvisioningContextKey(encrypted [240]byte) [16]byte {
	provisioningContextFieldCipher.Decrypt(encrypted[:], encrypted[:])
	defer clear(encrypted[:])
	return contextFormat.Mapping.importKey([16]byte(encrypted[:16]), encrypted[176:])
}

func encryptProvisioningContext(key, iv, associated [16]byte, input []byte) []byte {
	output := make([]byte, len(input)+sha256.Size)
	block, _ := aes.NewCipher(key[:])
	cipher.NewCBCEncrypter(block, iv[:]).CryptBlocks(output[:len(input)], input)
	hash := sha256.New()
	hash.Write(output[:len(input)])
	hash.Write(associated[:])
	hash.Write(contextFormat.Salt[:])
	return hash.Sum(output[:len(input)])
}

func decryptProvisioningContext(key, iv, transportKey [16]byte, input []byte) []byte {
	block, _ := aes.NewCipher(key[:])
	var payloadKey [16]byte
	block.Encrypt(payloadKey[:], transportKey[:])
	provisioningContextImportCipher.Encrypt(payloadKey[:], payloadKey[:])
	block, _ = aes.NewCipher(payloadKey[:])
	clear(payloadKey[:])
	output := make([]byte, len(input))
	cipher.NewCBCDecrypter(block, iv[:]).CryptBlocks(output, input)
	return output
}

// Each exchange copies the deterministic generator after 32 warm-up draws.
var provisioningSeed = cryptoutil.ReadAsset[struct {
	mt19937.SeedParameters
	MaskByte byte
}](formatAssets, "assets/provisioning-seed.bin")

var provisioningRandomStart = func() mt19937.Generator {
	r := provisioningSeed.NewGenerator()
	for range 32 {
		r.Next()
	}
	return r
}()

var provisioningRandomMask = bytes.Repeat([]byte{provisioningSeed.MaskByte}, 256)
