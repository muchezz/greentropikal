package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

// Vault encrypts every secret with AES-256-GCM under a key generated for that
// single secret. The key is never written to Redis and never stored anywhere
// on the server; it is handed back to the creator and travels in the URL
// fragment, which browsers do not send to the server. Storage therefore holds
// ciphertext the server cannot decrypt on its own.
//
// This is deliberately *not* described as end-to-end encryption: the plaintext
// and the key both pass through the server's memory while a secret is created
// and again while it is read.

const keySize = 32 // AES-256

var b64 = base64.RawURLEncoding

var errBadKey = errors.New("vault: key does not decrypt this secret")

func newKey() ([]byte, error) {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// newID returns a 128-bit random identifier. At that size an attacker cannot
// meaningfully enumerate stored secrets, which is what makes an unguessable
// URL a sound access control for this product.
func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b64.EncodeToString(b), nil
}

func encrypt(key, plaintext []byte) (string, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return b64.EncodeToString(aead.Seal(nonce, nonce, plaintext, nil)), nil
}

func decrypt(key []byte, blob string) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	raw, err := b64.DecodeString(blob)
	if err != nil {
		return nil, errBadKey
	}
	if len(raw) < aead.NonceSize() {
		return nil, errBadKey
	}
	nonce, ct := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		// GCM authentication failed: wrong key or tampered ciphertext. The
		// caller must not distinguish the two for the client.
		return nil, errBadKey
	}
	return plaintext, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != keySize {
		return nil, errBadKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// decodeKey parses the base64url key supplied by the viewer's browser.
func decodeKey(s string) ([]byte, error) {
	key, err := b64.DecodeString(s)
	if err != nil || len(key) != keySize {
		return nil, errBadKey
	}
	return key, nil
}
