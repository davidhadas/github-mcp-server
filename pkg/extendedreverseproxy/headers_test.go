// Copyright 2026 IBM. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package extendedreverseproxy

import (
	"net/http"
	"testing"
)

func TestRemoveHopByHopHeaders(t *testing.T) {
	h := http.Header{
		"Connection":          []string{"keep-alive"},
		"Keep-Alive":          []string{"timeout=5"},
		"Proxy-Connection":    []string{"keep-alive"},
		"Proxy-Authenticate":  []string{"Basic"},
		"Proxy-Authorization": []string{"Bearer token"},
		"Te":                  []string{"trailers"},
		"Trailer":             []string{"X-Trailer"},
		"Transfer-Encoding":   []string{"chunked"},
		"Upgrade":             []string{"websocket"},
		"X-Custom":            []string{"value"},
	}

	removeHopByHopHeaders(h)

	// Hop-by-hop headers should be removed
	hopByHop := []string{
		"Connection", "Keep-Alive", "Proxy-Connection",
		"Proxy-Authenticate", "Proxy-Authorization", "Te",
		"Trailer", "Transfer-Encoding", "Upgrade",
	}
	for _, header := range hopByHop {
		if h.Get(header) != "" {
			t.Errorf("expected %s to be removed", header)
		}
	}

	// Custom headers should remain
	if h.Get("X-Custom") != "value" {
		t.Error("expected X-Custom header to remain")
	}
}

func TestRemoveConnectionHeaders(t *testing.T) {
	h := http.Header{
		"Connection": []string{"X-Custom-1, X-Custom-2"},
		"X-Custom-1": []string{"value1"},
		"X-Custom-2": []string{"value2"},
		"X-Other":    []string{"other"},
	}

	removeConnectionHeaders(h)

	// Headers listed in Connection should be removed
	if h.Get("X-Custom-1") != "" {
		t.Error("expected X-Custom-1 to be removed")
	}
	if h.Get("X-Custom-2") != "" {
		t.Error("expected X-Custom-2 to be removed")
	}

	// Other headers should remain
	if h.Get("X-Other") != "other" {
		t.Error("expected X-Other to remain")
	}
}

func TestRemoveConnectionHeaders_MultipleValues(t *testing.T) {
	h := http.Header{
		"Connection": []string{"X-A", "X-B, X-C"},
		"X-A":        []string{"a"},
		"X-B":        []string{"b"},
		"X-C":        []string{"c"},
		"X-D":        []string{"d"},
	}

	removeConnectionHeaders(h)

	if h.Get("X-A") != "" {
		t.Error("expected X-A to be removed")
	}
	if h.Get("X-B") != "" {
		t.Error("expected X-B to be removed")
	}
	if h.Get("X-C") != "" {
		t.Error("expected X-C to be removed")
	}
	if h.Get("X-D") != "d" {
		t.Error("expected X-D to remain")
	}
}

func TestCopyHeaders(t *testing.T) {
	src := http.Header{
		"X-Single": []string{"value"},
		"X-Multi":  []string{"value1", "value2", "value3"},
	}
	dst := http.Header{}

	copyHeaders(dst, src)

	// Check single-value header
	if dst.Get("X-Single") != "value" {
		t.Error("expected X-Single to be copied")
	}

	// Check multi-value header
	values := dst["X-Multi"]
	if len(values) != 3 {
		t.Errorf("expected 3 values, got %d", len(values))
	}
	for i, expected := range []string{"value1", "value2", "value3"} {
		if values[i] != expected {
			t.Errorf("expected value %q at index %d, got %q", expected, i, values[i])
		}
	}
}

func TestCopyHeaders_PreservesExisting(t *testing.T) {
	src := http.Header{
		"X-Header": []string{"new"},
	}
	dst := http.Header{
		"X-Header": []string{"existing"},
	}

	copyHeaders(dst, src)

	// Should have both values
	values := dst["X-Header"]
	if len(values) != 2 {
		t.Errorf("expected 2 values, got %d", len(values))
	}
	if values[0] != "existing" || values[1] != "new" {
		t.Errorf("expected [existing, new], got %v", values)
	}
}

func TestAnnounceTrailers(t *testing.T) {
	resp := &http.Response{
		Trailer: http.Header{
			"X-Trailer-1": nil,
			"X-Trailer-2": nil,
		},
	}

	h := http.Header{}
	w := &mockResponseWriter{header: h}

	announceTrailers(w, resp)

	trailer := h.Get("Trailer")
	if trailer == "" {
		t.Fatal("expected Trailer header to be set")
	}

	// Should contain both trailer names (order may vary)
	if !contains(trailer, "X-Trailer-1") {
		t.Error("expected Trailer header to contain X-Trailer-1")
	}
	if !contains(trailer, "X-Trailer-2") {
		t.Error("expected Trailer header to contain X-Trailer-2")
	}
}

func TestAnnounceTrailers_NoTrailers(t *testing.T) {
	resp := &http.Response{
		Trailer: http.Header{},
	}

	h := http.Header{}
	w := &mockResponseWriter{header: h}

	announceTrailers(w, resp)

	if h.Get("Trailer") != "" {
		t.Error("expected no Trailer header when response has no trailers")
	}
}

func TestHeaderValuesContainsToken(t *testing.T) {
	tests := []struct {
		name     string
		values   []string
		token    string
		expected bool
	}{
		{
			name:     "single value match",
			values:   []string{"keep-alive"},
			token:    "keep-alive",
			expected: true,
		},
		{
			name:     "single value no match",
			values:   []string{"keep-alive"},
			token:    "close",
			expected: false,
		},
		{
			name:     "multiple tokens match",
			values:   []string{"keep-alive, Upgrade"},
			token:    "Upgrade",
			expected: true,
		},
		{
			name:     "case insensitive",
			values:   []string{"Keep-Alive"},
			token:    "keep-alive",
			expected: true,
		},
		{
			name:     "with whitespace",
			values:   []string{"  keep-alive  ,  close  "},
			token:    "close",
			expected: true,
		},
		{
			name:     "multiple values",
			values:   []string{"keep-alive", "Upgrade"},
			token:    "upgrade",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := headerValuesContainsToken(tt.values, tt.token)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestUpgradeType(t *testing.T) {
	tests := []struct {
		name     string
		headers  http.Header
		expected string
	}{
		{
			name: "websocket upgrade",
			headers: http.Header{
				"Connection": []string{"Upgrade"},
				"Upgrade":    []string{"websocket"},
			},
			expected: "websocket",
		},
		{
			name: "no upgrade",
			headers: http.Header{
				"Connection": []string{"keep-alive"},
			},
			expected: "",
		},
		{
			name: "upgrade without connection",
			headers: http.Header{
				"Upgrade": []string{"websocket"},
			},
			expected: "",
		},
		{
			name: "connection without upgrade",
			headers: http.Header{
				"Connection": []string{"Upgrade"},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := upgradeType(tt.headers)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// Helper types and functions

type mockResponseWriter struct {
	header http.Header
}

func (m *mockResponseWriter) Header() http.Header {
	return m.header
}

func (m *mockResponseWriter) Write([]byte) (int, error) {
	return 0, nil
}

func (m *mockResponseWriter) WriteHeader(int) {}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr)) ||
		len(s) > len(substr)+2 && s[1:len(s)-1] != s && containsMiddle(s, substr))
}

func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Made with Bob
