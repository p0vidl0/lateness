package secret

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestCipherRoundTrip(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	c, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := c.Seal([]byte("hunter2"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("hunter2")) {
		t.Fatal("plaintext leaked")
	}
	plain, err := c.Open(sealed)
	if err != nil || string(plain) != "hunter2" {
		t.Fatalf("%q %v", plain, err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := c.Open(sealed); err == nil {
		t.Fatal("tampered value opened")
	}
	other, _ := NewCipher(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)))
	sealed, _ = c.Seal([]byte("x"))
	if _, err := other.Open(sealed); err == nil {
		t.Fatal("opened with another key")
	}
}

func TestNewCipherRejectsShortKey(t *testing.T) {
	if _, err := NewCipher(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("accepted short key")
	}
}
