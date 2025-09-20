package crypto

import (
	"crypto/rand"
	"encoding/base64"

	"golang.org/x/crypto/argon2"
)

// GenRandomString generates a cryptographically secure URL and filename random token of the given size.
func GenRandomString(size int) (string, error) {
	b := make([]byte, size)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashPassword returns the hashed password and the salt used to hash it.
func HashPassword(password string) (string, string, error) {
	salt, err := GenRandomString(16)
	if err != nil {
		return "", "", err
	}
	hash := argon2.Key([]byte(password), []byte(salt), 3, 32*1024, 4, 32)
	return base64.URLEncoding.EncodeToString(hash), salt, nil
}

// ComparePasswords returns true if the plaintext password matches the given hash and salt when hashed.
func ComparePasswords(password, passHash, passSalt string) bool {
	hash := argon2.Key([]byte(password), []byte(passSalt), 3, 32*1024, 4, 32)
	return passHash == base64.URLEncoding.EncodeToString(hash)
}
