package utils

import "crypto/rand"

// RandomString generates n cryptographically random base32 characters.
func RandomString(n int) string {
	var s string
	for len(s) < n {
		s += rand.Text()
	}
	return s[:n]
}
