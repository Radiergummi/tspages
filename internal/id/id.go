// Package id provides unified resource ID generation for tspages.
package id

import "crypto/rand"

// base58 alphabet — excludes 0, O, I, l to avoid visual ambiguity.
const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

const idLen = 8

// New generates a prefixed ID: "{prefix}_{8 base58 chars}".
// Uses crypto/rand with rejection sampling (no modulo bias).
func New(prefix string) string {
	buf := make([]byte, idLen)
	raw := make([]byte, idLen*2) // over-read to reduce Read calls
	i := 0
	for i < idLen {
		rand.Read(raw)
		for _, b := range raw {
			b &= 0x3F // mask to 6 bits (0-63)
			if b < 58 {
				buf[i] = alphabet[b]
				i++
				if i == idLen {
					break
				}
			}
		}
	}
	return prefix + "_" + string(buf)
}
