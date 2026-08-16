package main

import (
	"testing"
	"time"
)

func TestTTLForSetUsesExplicitTTL(t *testing.T) {
	s := server{cfg: config{LRUIdleTTL: 10 * time.Minute}}
	ttlSeconds := int64(3)

	ttl, err := s.ttlForSet(&ttlSeconds)
	if err != nil {
		t.Fatalf("ttlForSet returned error: %v", err)
	}
	if ttl != 3*time.Second {
		t.Fatalf("ttl = %s, want 3s", ttl)
	}
}

func TestTTLForSetFallsBackToLRUIdleTTL(t *testing.T) {
	s := server{cfg: config{LRUIdleTTL: 10 * time.Minute}}

	ttl, err := s.ttlForSet(nil)
	if err != nil {
		t.Fatalf("ttlForSet returned error: %v", err)
	}
	if ttl != 10*time.Minute {
		t.Fatalf("ttl = %s, want 10m", ttl)
	}
}

func TestTTLForSetRejectsNegativeTTL(t *testing.T) {
	s := server{}
	ttlSeconds := int64(-1)

	_, err := s.ttlForSet(&ttlSeconds)
	if err == nil {
		t.Fatal("ttlForSet returned nil error for negative TTL")
	}
}

func TestTTLForSetReturnsZeroWhenLRUIdleTTLZero(t *testing.T) {
	s := server{cfg: config{LRUIdleTTL: 0}}

	ttl, err := s.ttlForSet(nil)
	if err != nil {
		t.Fatalf("ttlForSet returned error: %v", err)
	}
	if ttl != 0 {
		t.Fatalf("ttl = %s, want 0 (no expiration when LRUIdleTTL=0)", ttl)
	}
}

func TestValidateKeyRejectsEmpty(t *testing.T) {
	if err := validateKey(""); err == nil {
		t.Fatal("validateKey accepted empty key")
	}
}

func TestValidateKeyRejectsTooLong(t *testing.T) {
	key := make([]byte, maxKeyLen+1)
	for i := range key {
		key[i] = 'a'
	}
	if err := validateKey(string(key)); err == nil {
		t.Fatal("validateKey accepted over-length key")
	}
}

func TestValidateKeyRejectsBadChars(t *testing.T) {
	for _, k := range []string{"key with space", "key\"quote", "key\x00null", "key;inj", "key\ttab", "日本語の長いテキストー"} {
		if err := validateKey(k); err != nil {
			t.Fatalf("validateKey not accepted key %q", k)
		}
	}
}

func TestValidateValueRejectsBadChars(t *testing.T) {
	var v = "日本語の長いテキストー"
	if err := validateValue(v); err == nil {
		t.Fatalf("validateKey accepted value %q", v)
	}
}

func TestValidateKeyAcceptsCommonKeys(t *testing.T) {
	for _, k := range []string{"a", "foo", "trans:hello", "user/123", "a.b_c", "key-1", "Key.UPPER"} {
		if err := validateKey(k); err != nil {
			t.Fatalf("validateKey rejected good key %q: %v", k, err)
		}
	}
}

func TestValidateKeyRejectsAtMaxLenBoundary(t *testing.T) {
	key := make([]byte, maxKeyLen)
	for i := range key {
		key[i] = 'a'
	}
	if err := validateKey(string(key)); err != nil {
		t.Fatalf("validateKey rejected max-length (%d) key: %v", maxKeyLen, err)
	}
}
