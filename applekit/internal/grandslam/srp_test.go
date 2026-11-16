package grandslam

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"strings"
	"sync"
	"testing"
)

type vector struct {
	Name, Username, Password, Protocol, Salt, Private, Public string
	ServerPublic                                              string `json:"server_public"`
	Proof                                                     string
	ServerProof                                               string `json:"server_proof"`
	SessionKey                                                string `json:"session_key"`
	Iterations                                                int
}

func vectors(t *testing.T) []vector {
	t.Helper()
	raw, err := os.ReadFile("testdata/srp.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct{ Vectors []vector }
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	return file.Vectors
}

func unhex(t *testing.T, value string) []byte {
	t.Helper()
	b, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func prepare(t *testing.T, v vector) *Client {
	t.Helper()
	c, err := newClient(v.Username, v.Password, unhex(t, v.Private))
	if err != nil {
		t.Fatal(err)
	}
	proof, err := c.Challenge(Challenge{Salt: unhex(t, v.Salt), Public: unhex(t, v.ServerPublic), Iterations: v.Iterations, Protocol: v.Protocol})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(proof, unhex(t, v.Proof)) {
		t.Fatal("client proof differs from independent reference")
	}
	return c
}

func TestSrpIndependentVectors(t *testing.T) {
	for _, v := range vectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			c := prepare(t, v)
			if !bytes.Equal(c.Public(), unhex(t, v.Public)) {
				t.Fatal("client public differs from reference")
			}
			if !bytes.Equal(c.key, unhex(t, v.SessionKey)) {
				t.Fatal("session key differs from reference")
			}
			if !bytes.Equal(c.expectedProof, unhex(t, v.ServerProof)) {
				t.Fatal("server proof differs from reference")
			}
			if len(c.password) != 0 || c.secret.Sign() != 0 {
				t.Fatal("password or ephemeral secret retained after challenge")
			}
		})
	}
}

func TestSrpRejectsUnsafeChallenges(t *testing.T) {
	v := vectors(t)[0]

	valid := func() Challenge {
		return Challenge{Salt: unhex(t, v.Salt), Public: unhex(t, v.ServerPublic), Iterations: v.Iterations, Protocol: v.Protocol}
	}

	for _, change := range []func(*Challenge){
		func(c *Challenge) { c.Public = []byte{0} }, func(c *Challenge) { c.Public = modulus().Bytes() },
		func(c *Challenge) { c.Public = make([]byte, 257) }, func(c *Challenge) { c.Salt = nil },
		func(c *Challenge) { c.Iterations = 0 }, func(c *Challenge) { c.Iterations = 1_000_001 },
		func(c *Challenge) { c.Protocol = "unknown" },
	} {
		c, err := newClient(v.Username, v.Password, unhex(t, v.Private))
		if err != nil {
			t.Fatal(err)
		}
		ch := valid()
		change(&ch)
		if _, err := c.Challenge(ch); err == nil {
			t.Fatal("unsafe challenge accepted")
		}
		if _, err := c.Challenge(valid()); err == nil {
			t.Fatal("failed exchange reused")
		}
	}
}

func TestSrpEphemeralsAndConcurrentConsumption(t *testing.T) {
	first, err := NewClient("alice@example.test", "synthetic-password")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewClient("alice@example.test", "synthetic-password")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Public(), second.Public()) {
		t.Fatal("ephemeral key reused")
	}
	exported := first.Public()
	exported[0] ^= 0xff
	if bytes.Equal(exported, first.Public()) {
		t.Fatal("mutable public alias")
	}
	v := vectors(t)[0]
	c, err := newClient(v.Username, v.Password, unhex(t, v.Private))
	if err != nil {
		t.Fatal(err)
	}
	ch := Challenge{Salt: unhex(t, v.Salt), Public: unhex(t, v.ServerPublic), Iterations: v.Iterations, Protocol: v.Protocol}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { _, err := c.Challenge(ch); results <- err })
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatal("exchange consumed concurrently")
	}
}

func sessionFixture(t *testing.T) ([]byte, string) {
	t.Helper()
	raw, err := os.ReadFile("testdata/session.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct{ Ciphertext, Plaintext string }
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return unhex(t, f.Ciphertext), f.Plaintext
}

func TestVerifiedSessionAndPet(t *testing.T) {
	v := vectors(t)[0]
	encrypted, _ := sessionFixture(t)
	c := prepare(t, v)
	session, err := finishSrp(c, unhex(t, v.ServerProof), encrypted)
	if err != nil {
		t.Fatal(err)
	}
	pet, err := session.PasswordEquivalentToken()
	if err != nil {
		t.Fatal(err)
	}
	if pet != "synthetic-pet-value=PET" || session.Adsid != "synthetic-account-id" || session.DirectoryServicesId != "123456789" || session.Idms != "synthetic-idms-token" || session.Tokens["com.apple.gs.idms.hb"].Token != "synthetic-heartbeat" {
		t.Fatal("session fields changed")
	}
	if len(c.key) != 0 || len(c.expectedProof) != 0 {
		t.Fatal("key retained after completion")
	}
	if _, err := finishSrp(c, unhex(t, v.ServerProof), encrypted); err == nil {
		t.Fatal("completed exchange reused")
	}
}

func TestServerProofVerifiedBeforeDecryption(t *testing.T) {
	v := vectors(t)[0]
	c := prepare(t, v)
	if _, err := finishSrp(c, make([]byte, 32), []byte{1}); err == nil || !strings.Contains(err.Error(), "server proof") {
		t.Fatal("payload processed before verifying server proof")
	}
	encrypted, _ := sessionFixture(t)
	if _, err := finishSrp(c, unhex(t, v.ServerProof), encrypted); err == nil {
		t.Fatal("failed proof permitted a second attempt")
	}
}

func TestSessionCiphertextAndPaddingRejected(t *testing.T) {
	v := vectors(t)[0]
	encrypted, plain := sessionFixture(t)
	tampered := bytes.Clone(encrypted)
	padding := 16 - len(plain)%16
	tampered[len(tampered)-17] ^= byte(padding)
	for _, body := range [][]byte{nil, {1}, make([]byte, 1_048_576+16), tampered} {
		c := prepare(t, v)
		if _, err := finishSrp(c, unhex(t, v.ServerProof), body); err == nil {
			t.Fatal("invalid ciphertext accepted")
		}
	}
	if _, err := (SessionData{}).PasswordEquivalentToken(); err == nil {
		t.Fatal("2FA-pending session treated as PET-bearing")
	}
}

func TestAbandonedSrpExchange(t *testing.T) {
	v := vectors(t)[0]
	c := prepare(t, v)
	c.Close()
	c.Close()
	if len(c.key) != 0 || len(c.expectedProof) != 0 {
		t.Fatal("abandoned key retained")
	}
	if _, err := finishSrp(c, unhex(t, v.ServerProof), nil); err == nil {
		t.Fatal("abandoned exchange completed")
	}
	var zero Client
	zero.Close()
	if _, err := zero.Challenge(Challenge{}); err == nil {
		t.Fatal("zero exchange accepted")
	}
}

func TestSessionDirectoryIdentifierTypes(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  string
	}{
		{uint64(123_456_789), "123456789"}, {int64(123_456_789), "123456789"},
		{"123456789", "123456789"}, {uint64(18_446_744_073_709_551_615), "18446744073709551615"}, {nil, ""},
	} {
		got, err := directoryId(tc.value)
		if err != nil || got != tc.want {
			t.Fatalf("directory identifier conversion failed for %T", tc.value)
		}
	}
	for _, value := range []any{uint64(0), int64(-1), "", "-1", "1e3", "18446744073709551616", float64(123), true} {
		if _, err := directoryId(value); err == nil {
			t.Fatalf("invalid identifier type/value accepted: %T", value)
		}
	}
}

func finishSrp(c *Client, serverProof, encrypted []byte) (SessionData, error) {
	return c.FinishVerified(serverProof, encrypted, negotiationProof(c.key, c.protocol, encrypted, nil), nil)
}

func BenchmarkSrpModulus(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		benchmarkModulus = modulus()
	}
}

var benchmarkModulus *big.Int
