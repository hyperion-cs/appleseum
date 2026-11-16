package applebackemu

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"

	"howett.net/plist"
)

// RFC 5054 Appendix A, 2048-bit group; independent of the client's SRP implementation.
const (
	group2048 = "ac6bdb41324a9a9bf166de5e1389582faf72b6651987ee07fc3192943db56050a37329cbb4a099ed8193e0757767a13dd52312ab4b03310dcd7f48a9da04fd50e8083969edb767b0cf6095179a163ab3661a05fbd5faaae82918a9962f0b93b855f97993ec975eeaa80d740adbf4ff747359d041d5c33ea71d281e446b14773bca97b43a23fb801676bd207a436c6481f1d2b9078717461a5b9d32e688f87748544523b524b0d57d5ea77a2775d2ecfa032cfbdbf52fb3786160279004e57ae6af874e7303ce53299ccc041c7bc308d82a5698f3a8d0c38271ae35f8e9dbfbb694b5c803d89f7ae435de236d525f54759b65e372fcd68ef20fa7111f9e4aff73"
)

type GrandSlamOptions struct {
	Account               AuthAccount
	Device                AuthDevice
	Protocol              string // Empty selects s2k; s2k_fo is also supported.
	Iterations            int    // Zero selects 1000 for fast synthetic exchanges.
	RequireVerification   bool   // Omit PET and return secondaryAuth.
	RequireVerifiedCookie bool   // Require successful trusted-device confirmation.
}

// GrandSlam creates one init/complete exchange, accepting a fresh random client
// public value and checking its password proof. Each call owns its SRP state.
// Wrong passwords produce Apple's protocol error rather than a scenario error.
func GrandSlam(options GrandSlamOptions) ([]Step, error) {
	account := options.Account
	if account.AppleId == "" || account.Password == "" || account.Adsid == "" || account.Idms == "" || account.DirectoryServicesId == "" || (!options.RequireVerification && account.Pet == "") {
		return nil, fmt.Errorf("applebackemu: incomplete synthetic SRP account")
	}
	if options.Protocol == "" {
		options.Protocol = "s2k"
	}
	if options.Iterations == 0 {
		options.Iterations = 1_000
	}
	if (options.Protocol != "s2k" && options.Protocol != "s2k_fo") || options.Iterations < 1 || options.Iterations > 1_000_000 {
		return nil, fmt.Errorf("applebackemu: invalid SRP parameters")
	}
	init, err := GrandSlamRequest(options.Device, account.AppleId, "init", nil, Response{})
	if err != nil {
		return nil, err
	}
	complete, err := GrandSlamRequest(options.Device, account.AppleId, "complete", nil, Response{})
	if err != nil {
		return nil, err
	}
	var session *serverSrp
	var exchange string

	init.Respond = func(request Request) (Response, error) {
		if options.RequireVerifiedCookie {
			if err := requireCookie(request, "synthetic-factor", "verified"); err != nil {
				return Response{}, err
			}
		}
		body := grandSlamFields(request)
		public, _ := body["A2k"].([]byte)
		var salt [16]byte
		if _, err := rand.Read(salt[:]); err != nil {
			return Response{}, err
		}
		var err error
		session, err = newServerSrp(account.AppleId, account.Password, public, salt[:], options.Protocol, options.Iterations)
		if err != nil {
			return Response{}, err
		}
		exchange = hex.EncodeToString(salt[:])
		response, err := PlistResponse(map[string]any{"Response": map[string]any{
			"Status": map[string]any{"ec": 0}, "s": salt[:], "B": session.public,
			"i": options.Iterations, "sp": options.Protocol, "c": exchange,
		}})
		if err != nil {
			return Response{}, err
		}
		response.Header.Set("Set-Cookie", "gsa-synthetic=one; Path=/; Secure")
		return GzipResponse(response)
	}

	complete.Respond = func(request Request) (Response, error) {
		if session != nil {
			defer clear(session.key)
		}
		if err := requireCookie(request, "gsa-synthetic", "one"); err != nil {
			return Response{}, err
		}
		body := grandSlamFields(request)
		proof, _ := body["M1"].([]byte)
		if session == nil || body["c"] != exchange || !hmac.Equal(proof, session.proof) {
			return PlistResponse(map[string]any{"Response": map[string]any{"Status": map[string]any{"ec": -22_406}}})
		}
		status := map[string]any{"ec": 0}
		data := map[string]any{"adsid": account.Adsid, "GsIdmsToken": account.Idms, "DsPrsId": account.DirectoryServicesId}
		if options.RequireVerification {
			status["au"] = "secondaryAuth"
		} else {
			data["t"] = map[string]any{"com.apple.gs.idms.pet": map[string]any{"token": account.Pet}}
		}
		encrypted, err := encryptSession(session.key, data)
		if err != nil {
			return Response{}, err
		}
		return PlistResponse(map[string]any{"Response": map[string]any{
			"Status": status, "M2": session.serverProof, "spd": encrypted,
			"np": serverNegotiationProof(session.key, options.Protocol, encrypted),
		}})
	}

	return []Step{init, complete}, nil
}

func grandSlamFields(request Request) map[string]any {
	var envelope struct{ Request map[string]any }
	plist.Unmarshal(request.Body, &envelope) // Shape was checked before Respond.
	return envelope.Request
}

type serverSrp struct{ public, key, proof, serverProof []byte }

func digest(parts ...[]byte) []byte {
	h := sha256.New()
	for _, part := range parts {
		h.Write(part)
	}
	return h.Sum(nil)
}

func newServerSrp(username, password string, clientPublic, salt []byte, protocol string, iterations int) (*serverSrp, error) {
	n, _ := new(big.Int).SetString(group2048, 16)
	g := big.NewInt(2)
	A := new(big.Int).SetBytes(clientPublic)
	if A.Sign() == 0 || A.Cmp(n) >= 0 {
		return nil, fmt.Errorf("invalid SRP client public value")
	}
	initial := sha256.Sum256([]byte(password))
	passwordInput := string(initial[:])
	if protocol == "s2k_fo" {
		passwordInput = hex.EncodeToString(initial[:])
	}
	derived, err := pbkdf2.Key(sha256.New, passwordInput, salt, iterations, 32)
	if err != nil {
		return nil, err
	}
	x := new(big.Int).SetBytes(digest(salt, digest([]byte(":"), derived)))
	clear(derived)
	verifier := new(big.Int).Exp(g, x, n)
	secret, err := rand.Int(rand.Reader, new(big.Int).Sub(n, big.NewInt(1)))
	if err != nil {
		return nil, err
	}
	secret.Add(secret, big.NewInt(1))

	padded := func(value *big.Int) []byte { return value.FillBytes(make([]byte, 256)) }

	k := new(big.Int).SetBytes(digest(padded(n), padded(g)))
	B := new(big.Int).Mul(k, verifier)
	B.Add(B, new(big.Int).Exp(g, secret, n)).Mod(B, n)
	u := new(big.Int).SetBytes(digest(padded(A), padded(B)))
	shared := new(big.Int).Exp(verifier, u, n)
	shared.Mul(shared, A).Mod(shared, n)
	shared.Exp(shared, secret, n)
	key := digest(shared.Bytes())
	groupHash, generatorHash := digest(padded(n)), digest(padded(g))
	for i := range groupHash {
		groupHash[i] ^= generatorHash[i]
	}
	proof := digest(groupHash, digest([]byte(username)), salt, A.Bytes(), B.Bytes(), key)
	return &serverSrp{B.Bytes(), key, proof, digest(A.Bytes(), proof, key)}, nil
}

func keyedDigest(key []byte, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func encryptSession(key []byte, fields map[string]any) ([]byte, error) {
	plain, err := plist.Marshal(fields, plist.XMLFormat)
	if err != nil {
		return nil, err
	}
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(plain, bytes.Repeat([]byte{byte(padding)}, padding)...)
	block, err := aes.NewCipher(keyedDigest(key, []byte("extra data key:")))
	if err != nil {
		return nil, err
	}
	iv := keyedDigest(key, []byte("extra data iv:"))
	encrypted := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, iv[:aes.BlockSize]).CryptBlocks(encrypted, plain)
	clear(plain)
	return encrypted, nil
}

func serverNegotiationProof(key []byte, protocol string, encrypted []byte) []byte {
	transcript := []byte("s2k,s2k_fo||" + protocol + "|")
	transcript = binary.LittleEndian.AppendUint32(transcript, uint32(len(encrypted)))
	transcript = append(transcript, encrypted...)
	transcript = append(transcript, '|', '|') // No sc field in these scenarios.
	return keyedDigest(keyedDigest(key, []byte("HMAC key:")), digest(transcript))
}
