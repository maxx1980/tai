// Package backup implements the master-password primitives for webssh: argon2id
// password hashing (for the unlock gate) and password-based authenticated
// encryption (argon2id -> AES-256-GCM) used for encrypted settings backups.
package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/argon2"
)

// ErrWrongPassword is returned by Decrypt when the password is wrong or the
// ciphertext has been tampered with (GCM authentication failure).
var ErrWrongPassword = errors.New("wrong password or corrupt backup")

// argon2id cost parameters (shared by hashing and key derivation).
const (
	argonTime    = 1
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 4
	argonKeyLen  = 32 // 256-bit key / hash
	saltLen      = 16
)

// deriveKey stretches a password into a 32-byte key using argon2id and salt.
func deriveKey(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, err
	}
	return b, nil
}

// HashPassword returns an encoded argon2id hash of the form
// "argon2id$<b64salt>$<b64hash>" for storage in the settings table.
func HashPassword(password string) (string, error) {
	salt, err := randomBytes(saltLen)
	if err != nil {
		return "", err
	}
	hash := deriveKey(password, salt)
	return fmt.Sprintf("argon2id$%s$%s",
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash)), nil
}

// VerifyPassword reports whether password matches the encoded argon2id hash.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 3 || parts[0] != "argon2id" {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	got := deriveKey(password, salt)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// envelope is the on-disk JSON format for an encrypted backup.
type envelope struct {
	V     int    `json:"v"`
	Salt  string `json:"salt"`  // base64, argon2id salt
	Nonce string `json:"nonce"` // base64, GCM nonce
	CT    string `json:"ct"`    // base64, ciphertext+tag
}

// Encrypt derives a key from password (with a fresh random salt) and seals
// plaintext with AES-256-GCM, returning a JSON envelope.
func Encrypt(password string, plaintext []byte) ([]byte, error) {
	salt, err := randomBytes(saltLen)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(deriveKey(password, salt))
	if err != nil {
		return nil, err
	}
	nonce, err := randomBytes(gcm.NonceSize())
	if err != nil {
		return nil, err
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	env := envelope{
		V:     1,
		Salt:  base64.StdEncoding.EncodeToString(salt),
		Nonce: base64.StdEncoding.EncodeToString(nonce),
		CT:    base64.StdEncoding.EncodeToString(ct),
	}
	return json.MarshalIndent(env, "", "  ")
}

// Decrypt parses a JSON envelope, derives the key from password, and opens the
// ciphertext. Returns ErrWrongPassword on any authentication/parse failure.
func Decrypt(password string, data []byte) ([]byte, error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil || env.V != 1 {
		return nil, ErrWrongPassword
	}
	salt, err1 := base64.StdEncoding.DecodeString(env.Salt)
	nonce, err2 := base64.StdEncoding.DecodeString(env.Nonce)
	ct, err3 := base64.StdEncoding.DecodeString(env.CT)
	if err1 != nil || err2 != nil || err3 != nil {
		return nil, ErrWrongPassword
	}
	gcm, err := newGCM(deriveKey(password, salt))
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, ErrWrongPassword
	}
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, ErrWrongPassword
	}
	return pt, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
