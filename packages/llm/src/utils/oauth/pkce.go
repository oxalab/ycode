package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

func base64URLEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// GeneratePKCE: Creates a PKCE code verifier and SHA-256 challenge pair.
func GeneratePKCE() (verifier string, challenge string, err error) {
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return "", "", err
	}
	verifier = base64URLEncode(verifierBytes)
	hash := sha256.Sum256([]byte(verifier))
	challenge = base64URLEncode(hash[:])
	return verifier, challenge, err
}
