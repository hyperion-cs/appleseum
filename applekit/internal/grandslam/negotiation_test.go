package grandslam

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type negotiationVector struct {
	Name, Key, Protocol, Encrypted, Proof string
	ServerContext                         *string `json:"server_context"`
}

func negotiationVectors(t *testing.T) []negotiationVector {
	t.Helper()
	raw, err := os.ReadFile("testdata/negotiation.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct{ Vectors []negotiationVector }
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	return file.Vectors
}

func serverContext(t *testing.T, v negotiationVector) []byte {
	t.Helper()
	if v.ServerContext == nil {
		return nil
	}
	data := unhex(t, *v.ServerContext)
	if data == nil {
		return []byte{}
	}
	return data
}

func TestNegotiationIndependentVectors(t *testing.T) {
	for _, v := range negotiationVectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			got := negotiationProof(unhex(t, v.Key), v.Protocol, unhex(t, v.Encrypted), serverContext(t, v))
			if !bytes.Equal(got, unhex(t, v.Proof)) {
				t.Fatal("negotiation proof differs from independent OpenSSL result")
			}
		})
	}
}

func TestFinishVerifiesNegotiationBeforeDecrypting(t *testing.T) {
	srp := vectors(t)[0]
	np := negotiationVectors(t)[0]
	for _, change := range []func(*Client, *[]byte, *[]byte, *[]byte){
		func(c *Client, encrypted, proof, sc *[]byte) { *proof = make([]byte, 32) },
		func(c *Client, encrypted, proof, sc *[]byte) { *proof = nil },
		func(c *Client, encrypted, proof, sc *[]byte) { (*encrypted)[0] ^= 1 },
		func(c *Client, encrypted, proof, sc *[]byte) { *encrypted = []byte{1} },
		func(c *Client, encrypted, proof, sc *[]byte) { *sc = []byte{} },
		func(c *Client, encrypted, proof, sc *[]byte) { c.protocol = protocolS2kfo },
	} {
		c := prepare(t, srp)
		encrypted, proof, sc := unhex(t, np.Encrypted), unhex(t, np.Proof), serverContext(t, np)
		change(c, &encrypted, &proof, &sc)
		data, err := c.FinishVerified(unhex(t, srp.ServerProof), encrypted, proof, sc)
		if err == nil || !strings.Contains(err.Error(), "negotiation proof") || data.Adsid != "" {
			t.Fatal("bad transcript released a session or reached decryption")
		}
		if len(c.key) != 0 || len(c.expectedProof) != 0 || c.protocol != "" {
			t.Fatal("failed negotiation retained exchange state")
		}
		if _, err := finishSrp(c, unhex(t, srp.ServerProof), encrypted); err == nil {
			t.Fatal("failed negotiation could fall back to M2-only verification")
		}
	}
}

func TestFinishVerifiedAcceptsOptionalServerContext(t *testing.T) {
	srp := vectors(t)[0]
	for _, v := range negotiationVectors(t)[:3] {
		c := prepare(t, srp)
		data, err := c.FinishVerified(unhex(t, srp.ServerProof), unhex(t, v.Encrypted), unhex(t, v.Proof), serverContext(t, v))
		if err != nil || data.Adsid != "synthetic-account-id" {
			t.Fatalf("verified session rejected for %s: %v", v.Name, err)
		}
	}
	c := prepare(t, srp)
	if _, err := c.FinishVerified(make([]byte, 32), []byte{1}, nil, nil); err == nil || !strings.Contains(err.Error(), "server proof") {
		t.Fatal("server proof not checked before negotiation/decryption")
	}
}
