package grandslam

import (
	"bytes"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"strings"
	"sync"
)

const (
	srpGroupSize      = 256
	srpSecretSize     = 32
	maxUsernameLength = 1_024
	maxPasswordLength = 16_384
	maxSaltLength     = 1_024
	maxIterations     = 1_000_000
	protocolS2k       = "s2k"
	protocolS2kfo     = "s2k_fo"
)

// RFC 5054, Appendix A, 2048-bit SRP group; generator 2.
//
//go:embed assets/srp-2048-prime.bin
var srpPrime []byte

// Never mutate the shared group parameters.
var srpModulus = func() *big.Int {
	if len(srpPrime) != srpGroupSize {
		panic("grandslam: invalid embedded SRP-2048 prime")
	}
	return new(big.Int).SetBytes(srpPrime)
}()
var srpGenerator = big.NewInt(2)
var srpPaddedGenerator = srpGenerator.FillBytes(make([]byte, srpGroupSize))
var srpMultiplier = integerHash(srpPrime, srpPaddedGenerator)
var srpGroupHash = func() []byte {
	out := hash(srpPrime)
	generatorHash := hash(srpPaddedGenerator)
	subtle.XORBytes(out, out, generatorHash)
	return out
}()

type Challenge struct {
	Salt       []byte
	Public     []byte
	Iterations int
	Protocol   string
}

func passwordProtocols() []string { return []string{protocolS2k, protocolS2kfo} }

// Challenge and Finish consume their stage once, including on failure.
type Client struct {
	mu            sync.Mutex
	username      string
	password      []byte
	secret        *big.Int
	public        []byte
	key           []byte
	expectedProof []byte
	protocol      string
	stage         uint8
}

func NewClient(username, password string) (*Client, error) {
	for {
		secret := make([]byte, srpSecretSize)
		if _, err := io.ReadFull(rand.Reader, secret); err != nil {
			return nil, err
		}
		if bytes.Equal(secret, make([]byte, srpSecretSize)) {
			continue
		}
		client, err := newClient(username, password, secret)
		clear(secret)
		return client, err
	}
}

func newClient(username, password string, secret []byte) (*Client, error) {
	if len(username) == 0 || len(username) > maxUsernameLength || len(password) == 0 || len(password) > maxPasswordLength || len(secret) != srpSecretSize {
		return nil, errors.New("grandslam: invalid SRP credentials or secret")
	}
	a := new(big.Int).SetBytes(secret)
	if a.Sign() == 0 {
		return nil, errors.New("grandslam: zero SRP secret")
	}
	A := new(big.Int).Exp(srpGenerator, a, modulus())
	return &Client{username: username, password: []byte(password), secret: a, public: A.Bytes()}, nil
}

// Public returns A2k in minimal big-endian encoding.
func (c *Client) Public() []byte { return bytes.Clone(c.public) }

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stage = 2
	clear(c.password)
	c.password = nil
	if c.secret != nil {
		c.secret.SetInt64(0)
	}
	clear(c.key)
	c.key = nil
	clear(c.expectedProof)
	c.expectedProof = nil
	c.protocol = ""
}

func (c *Client) Challenge(ch Challenge) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage != 0 || c.secret == nil {
		return nil, errors.New("grandslam: SRP challenge already consumed")
	}
	c.stage = 1
	defer func() { clear(c.password); c.password = nil; c.secret.SetInt64(0) }()
	if len(ch.Salt) == 0 || len(ch.Salt) > maxSaltLength || len(ch.Public) == 0 || len(ch.Public) > srpGroupSize || ch.Iterations < 1 || ch.Iterations > maxIterations {
		return nil, errors.New("grandslam: invalid SRP challenge")
	}
	n := modulus()
	g := srpGenerator
	B := new(big.Int).SetBytes(ch.Public)
	if B.Sign() == 0 || B.Cmp(n) >= 0 {
		return nil, errors.New("grandslam: invalid SRP server public value")
	}
	initial := sha256.Sum256(c.password)
	input := initial[:]
	switch ch.Protocol {
	case protocolS2k:
	case protocolS2kfo:
		input = []byte(hex.EncodeToString(input))
	default:
		return nil, errors.New("grandslam: unsupported password protocol")
	}
	password, err := pbkdf2.Key(sha256.New, string(input), ch.Salt, ch.Iterations, sha256.Size)
	if err != nil {
		return nil, err
	}
	defer clear(password)
	// Username is omitted only from x; the colon and username hash in M1 remain.
	x := integerHash(ch.Salt, hash([]byte(":"), password))
	k := srpMultiplier
	A := new(big.Int).SetBytes(c.public)
	// u uses padded A/B; M1/M2 use minimal encodings.
	u := integerHash(A.FillBytes(make([]byte, srpGroupSize)), B.FillBytes(make([]byte, srpGroupSize)))
	if u.Sign() == 0 {
		return nil, errors.New("grandslam: zero SRP scrambling parameter")
	}
	gx := new(big.Int).Exp(g, x, n)
	base := new(big.Int).Mul(k, gx)
	base.Sub(B, base).Mod(base, n)
	exponent := new(big.Int).Mul(u, x)
	exponent.Add(exponent, c.secret)
	shared := new(big.Int).Exp(base, exponent, n)
	if shared.Sign() == 0 {
		return nil, errors.New("grandslam: zero SRP shared secret")
	}
	key := hash(shared.Bytes())
	proof := hash(srpGroupHash, hash([]byte(c.username)), ch.Salt, c.public, B.Bytes(), key)
	c.expectedProof = hash(c.public, proof, key)
	c.key = key
	c.protocol = ch.Protocol
	return proof, nil
}

// FinishVerified checks M2/np before decrypting.
// serverContext is nil when absent, non-nil and empty for a present empty field.
func (c *Client) FinishVerified(serverProof, encrypted, negotiation, serverContext []byte) (SessionData, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage != 1 || len(c.key) != sha256.Size {
		return SessionData{}, errors.New("grandslam: no SRP session awaiting verification")
	}
	c.stage = 2
	defer func() { clear(c.key); c.key = nil; clear(c.expectedProof); c.expectedProof = nil; c.protocol = "" }()
	if !hmac.Equal(serverProof, c.expectedProof) {
		return SessionData{}, errors.New("grandslam: invalid server proof")
	}
	if len(negotiation) != sha256.Size || len(encrypted) == 0 || len(encrypted) > 1_048_576 || len(serverContext) > 1_048_576 ||
		!hmac.Equal(negotiation, negotiationProof(c.key, c.protocol, encrypted, serverContext)) {
		return SessionData{}, errors.New("grandslam: invalid negotiation proof")
	}
	return decryptSession(c.key, encrypted)
}

// Apple's transcript prefixes data with LE32 lengths but uses raw UTF-8 strings.
func negotiationProof(key []byte, protocol string, encrypted, serverContext []byte) []byte {
	transcript := sha256.New()
	transcript.Write([]byte(strings.Join(passwordProtocols(), ",") + "||" + protocol + "|"))

	addData := func(data []byte) {
		var length [4]byte
		binary.LittleEndian.PutUint32(length[:], uint32(len(data)))
		transcript.Write(length[:])
		transcript.Write(data)
	}

	addData(encrypted)
	transcript.Write([]byte("|"))
	if serverContext != nil {
		addData(serverContext)
	}
	transcript.Write([]byte("|"))
	hmacKey := deriveKey(key, "HMAC key:")
	defer clear(hmacKey)
	mac := hmac.New(sha256.New, hmacKey)
	mac.Write(transcript.Sum(nil))
	return mac.Sum(nil)
}

func modulus() *big.Int { return new(big.Int).Set(srpModulus) }

func hash(parts ...[]byte) []byte {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

func integerHash(parts ...[]byte) *big.Int { return new(big.Int).SetBytes(hash(parts...)) }
