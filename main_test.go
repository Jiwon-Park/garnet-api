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
