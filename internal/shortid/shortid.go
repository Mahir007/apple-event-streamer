// Package shortid generates "short UUIDs": random version 4 UUIDs encoded
// in the 57-character shortuuid alphabet (no 0/O/1/I/l), giving 22 URL- and
// filename-safe characters.
package shortid

import (
	"crypto/rand"
	"math/big"
)

const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// Length is the length of every generated ID.
const Length = 22

// New returns a new random short UUID.
func New() string {
	var u [16]byte
	rand.Read(u[:])         // never returns an error since Go 1.24
	u[6] = u[6]&0x0f | 0x40 // version 4
	u[8] = u[8]&0x3f | 0x80 // RFC 4122 variant
	return Encode(u)
}

// Encode encodes a 128-bit UUID, least significant digit last, padded to
// Length characters.
func Encode(u [16]byte) string {
	n := new(big.Int).SetBytes(u[:])
	base := big.NewInt(int64(len(alphabet)))
	out := make([]byte, Length)
	for i := Length - 1; i >= 0; i-- {
		var rem big.Int
		n.DivMod(n, base, &rem)
		out[i] = alphabet[rem.Int64()]
	}
	return string(out)
}
