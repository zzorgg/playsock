package server

import "testing"

func TestConnectionLimiter(t *testing.T) {
	limiter := newConnectionLimiter(1)
	if limiter == nil {
		t.Fatalf("expected limiter instance")
	}

	release, ok := limiter.acquire("10.0.0.1")
	if !ok {
		t.Fatalf("first acquire should succeed")
	}

	if _, ok := limiter.acquire("10.0.0.1"); ok {
		t.Fatalf("second acquire without release should fail")
	}

	release()

	if _, ok := limiter.acquire("10.0.0.1"); !ok {
		t.Fatalf("acquire after release should succeed")
	}
}

func TestExtractBearer(t *testing.T) {
	token := extractBearer("Bearer secret")
	if token != "secret" {
		t.Fatalf("expected token 'secret', got %q", token)
	}

	if other := extractBearer("bearer another"); other != "another" {
		t.Fatalf("expected case-insensitive bearer, got %q", other)
	}

	if invalid := extractBearer("Token abc"); invalid != "" {
		t.Fatalf("expected empty token for invalid header, got %q", invalid)
	}

	if empty := extractBearer(""); empty != "" {
		t.Fatalf("expected empty token when header absent, got %q", empty)
	}
}
