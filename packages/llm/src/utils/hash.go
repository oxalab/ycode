package utils

import (
	"math/bits"
	"strconv"
)

// ShortHash: Produces a fast deterministic hash to shorten long strings.

func ShortHash(s string) string {
	h1 := uint32(0xdeadbeef)
	h2 := uint32(0x41c6ce57)
	for i := 0; i < len(s); i++ {
		ch := uint32(s[i])
		h1 = bits.RotateLeft32(h1^ch, 0) * 2654435761
		h2 = bits.RotateLeft32(h2^ch, 0) * 1597334677
	}
	h1 = bits.RotateLeft32(h1^(h1>>16), 0)*2246822507 ^ bits.RotateLeft32(h2^(h2>>13), 0)*3266489909
	h2 = bits.RotateLeft32(h2^(h2>>16), 0)*2246822507 ^ bits.RotateLeft32(h1^(h1>>13), 0)*3266489909
	return strconv.FormatUint(uint64(h2), 36) + strconv.FormatUint(uint64(h1), 36)
}