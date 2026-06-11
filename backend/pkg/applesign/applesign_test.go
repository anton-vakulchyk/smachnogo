package applesign

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestNonceMatches(t *testing.T) {
	raw := "random-client-nonce"
	sum := sha256.Sum256([]byte(raw))
	hashed := hex.EncodeToString(sum[:])

	if !NonceMatches(hashed, raw) {
		t.Fatal("matching nonce must pass")
	}
	if NonceMatches(hashed, "wrong") {
		t.Fatal("wrong raw nonce must fail")
	}
	if NonceMatches(hashed, "") {
		t.Fatal("missing raw nonce must fail when token carries one")
	}
	if !NonceMatches("", "") {
		t.Fatal("no nonce anywhere is fine (client didn't request one)")
	}
}
