//go:build linux && 386 && ishcrypto

package ish

import (
	"crypto/cipher"
	"crypto/subtle"
	"encoding/binary"
	"sync"
)

// ishGhash tables are indexed only by public ciphertext/AAD bytes.
// Secret accumulated hashes use fixed-size multiplication without table lookups.
type ishGhash struct {
	values [][4]uint32
	powers [][4]uint32
	max    int
}

func newIshGhash(H *[16]byte, max int) *ishGhash {
	t := &ishGhash{values: make([][4]uint32, max*4_096), powers: make([][4]uint32, max), max: max}
	power := *H
	for k := range max {
		if k > 0 {
			var next [16]byte
			cipher.GhashIsh(&next, H, power[:])
			power = next
		}
		v := [4]uint32{binary.BigEndian.Uint32(power[0:]), binary.BigEndian.Uint32(power[4:]), binary.BigEndian.Uint32(power[8:]), binary.BigEndian.Uint32(power[12:])}
		t.powers[k] = v
		for pos := 0; pos < 16; pos++ {
			var basis [8][4]uint32
			for bit := 0; bit < 8; bit++ {
				basis[bit] = v
				mask := uint32(0) - (v[3] & 1)
				v = [4]uint32{v[0]>>1 ^ (mask & 0xe1000000), v[1]>>1 | v[0]<<31, v[2]>>1 | v[1]<<31, v[3]>>1 | v[2]<<31}
			}
			row := t.values[k*4_096+pos*256 : k*4_096+(pos+1)*256]
			for bit := 0; bit < 8; bit++ {
				limit := 1 << bit
				b := basis[7-bit]
				for digit := 0; digit < limit; digit++ {
					v := row[digit]
					row[limit+digit] = [4]uint32{v[0] ^ b[0], v[1] ^ b[1], v[2] ^ b[2], v[3] ^ b[3]}
				}
			}
		}
	}
	return t
}

//go:noescape
func lookupIsh(table *[4]uint32, input *[16]byte, blocks int, output *[4]uint32)

func (t *ishGhash) sum(out *[16]byte, inputs ...[]byte) {
	total := 0
	for _, input := range inputs {
		total += (len(input) + 15) / 16
	}
	chunk := min(total, t.max)
	remaining := chunk
	first := true
	var previous, acc [4]uint32
	for _, input := range inputs {
		for len(input) > 0 {
			n := min(len(input)/16, remaining)
			var partial [16]byte
			data := input
			if n == 0 {
				copy(partial[:], input)
				data = partial[:]
				input = nil
				n = 1
			} else {
				input = input[n*16:]
			}
			var contribution [4]uint32
			lookupIsh(&t.values[(remaining-1)*4_096], (*[16]byte)(data), n, &contribution)
			for i := range acc {
				acc[i] ^= contribution[i]
			}
			remaining -= n
			if remaining == 0 {
				if !first {
					multiplyIsh(&previous, &t.powers[chunk-1], &previous)
				}
				for i := range previous {
					previous[i] ^= acc[i]
				}
				acc = [4]uint32{}
				first = false
				total -= chunk
				chunk = min(total, t.max)
				remaining = chunk
			}
		}
	}
	for i, v := range previous {
		binary.BigEndian.PutUint32(out[i*4:], v)
	}
}

type ishGcm struct {
	key   *ishAes
	table *ishGhash
	pool  *ishWorkers
}

type ishRecord struct {
	g             *ishAes
	out, ct       []byte
	nonce         [12]byte
	blocks, parts int
	wg            sync.WaitGroup
}

// Workers are shared and block while idle; no sockets or record keys are retained.
type ishWorkers struct {
	queues  []chan *ishRecord
	records sync.Pool
}

func newIshWorkers(n int) *ishWorkers {
	n = min(max(n, 1), 6)
	p := &ishWorkers{queues: make([]chan *ishRecord, n-1)}

	p.records.New = func() any { return new(ishRecord) }

	for i := range p.queues {
		q := make(chan *ishRecord, 1)
		p.queues[i] = q
		go func(i int) {
			for r := range q {
				r.decrypt(i)
				r.wg.Done()
			}
		}(i + 1)
	}
	return p
}

func (r *ishRecord) decrypt(i int) {
	start, end := r.blocks*i/r.parts, r.blocks*(i+1)/r.parts
	if end > start {
		var counter [16]byte
		copy(counter[:], r.nonce[:])
		binary.BigEndian.PutUint32(counter[12:], uint32(2+start))
		last := min(len(r.ct), end*16)
		counterCryptIsh(r.g, r.out[start*16:last], r.ct[start*16:last], &counter)
	}
}

// openIsh receives validated TLS records from standard GCM and authenticates
// the complete record before decrypting any bytes.
func (p *ishGcm) openIsh(out, nonce, sealed, aad []byte) bool {
	ct, tag := sealed[:len(sealed)-16], sealed[len(sealed)-16:]
	var lengths, digest, counter, mask [16]byte
	binary.BigEndian.PutUint64(lengths[:], uint64(len(aad))*8)
	binary.BigEndian.PutUint64(lengths[8:], uint64(len(ct))*8)
	p.table.sum(&digest, aad, ct, lengths[:])
	copy(counter[:], nonce)
	counter[15] = 1
	p.key.view.Encrypt(mask[:], counter[:])
	subtle.XORBytes(digest[:], digest[:], mask[:])
	if subtle.ConstantTimeCompare(digest[:], tag) != 1 {
		return false
	}
	r := p.pool.records.Get().(*ishRecord)
	r.g = p.key
	r.out = out
	r.ct = ct
	copy(r.nonce[:], nonce)
	r.blocks = (len(ct) + 15) / 16
	r.parts = min(len(p.pool.queues)+1, r.blocks)
	r.wg.Add(r.parts - 1)
	for i := 1; i < r.parts; i++ {
		p.pool.queues[i-1] <- r
	}
	r.decrypt(0)
	r.wg.Wait()
	r.g = nil
	r.out = nil
	r.ct = nil
	r.nonce = [12]byte{}
	p.pool.records.Put(r)
	return true
}
