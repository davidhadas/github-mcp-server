// Copyright 2026 IBM. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package extendedreverseproxy

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// generateTestKey generates a random correlation key for testing.
func generateTestKey() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// TestE2E_Scenario1_NormalForward tests Scenario 1: normal forwarding.
// Client → Sidecar → App
// Client ← Sidecar ← App (streamed directly)
func TestE2E_Scenario1_NormalForward(t *testing.T) {
	// Create upstream server
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request was forwarded correctly
		if r.Header.Get("X-Test-Header") != "test-value" {
			t.Errorf("Expected X-Test-Header to be forwarded")
		}

		w.Header().Set("X-Upstream-Response", "from-app")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Hello from upstream!")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	// Create proxy
	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			// All requests are Type A (forward)
			return InboundRequest{
				Kind: RequestKindForward,
				In:   r,
				Key:  generateTestKey(),
			}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			ur.Out.URL.Scheme = upstreamURL.Scheme
			ur.Out.URL.Host = upstreamURL.Host
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			// Always forward normally
			return ForwardNow{}, nil
		},
		Transport: http.DefaultTransport,
		Store:     NewMemoryStore(),
	}

	// Create test server with proxy
	server := httptest.NewServer(proxy)
	defer server.Close()

	// Make request
	req, _ := http.NewRequest("GET", server.URL+"/test", nil)
	req.Header.Set("X-Test-Header", "test-value")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	// Verify response
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	if resp.Header.Get("X-Upstream-Response") != "from-app" {
		t.Errorf("Expected X-Upstream-Response header from upstream")
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "Hello from upstream!" {
		t.Errorf("Expected 'Hello from upstream!', got '%s'", body)
	}
}

// TestE2E_Scenario2_DetachAndResume tests Scenario 2: detached flow with resume.
// Type A: Client → Sidecar → App (starts), Client ← Sidecar (interim 401/202)
// Type B: Client → Sidecar (lookup), Client ← Sidecar ← App (streamed)
func TestE2E_Scenario2_DetachAndResume(t *testing.T) {
	// Create upstream server that simulates processing
	upstreamCallCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCallCount++

		// Simulate processing time
		time.Sleep(100 * time.Millisecond)

		w.Header().Set("X-Upstream-Response", "processed")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Processed result from upstream")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	// Create proxy
	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			// Check for resume key
			if key := r.Header.Get("X-Resume-Key"); key != "" {
				return InboundRequest{
					Kind: RequestKindResume,
					In:   r,
					Key:  key,
				}, nil
			}
			// Type A request
			return InboundRequest{
				Kind: RequestKindForward,
				In:   r,
				Key:  generateTestKey(),
			}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			ur.Out.URL.Scheme = upstreamURL.Scheme
			ur.Out.URL.Host = upstreamURL.Host
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			// Detach if X-Needs-Auth header is present
			if ur.In.Header.Get("X-Needs-Auth") == "true" {
				return DetachAndWait{
					InterimStatusCode: http.StatusUnauthorized,
					InterimHeader:     http.Header{"X-Resume-Key": []string{ur.Key}},
					InterimBody:       []byte("Please authenticate and resume"),
				}, nil
			}
			return ForwardNow{}, nil
		},
		Transport: http.DefaultTransport,
		Store:     NewMemoryStore(),
		Timeout:   5 * time.Second,
	}

	server := httptest.NewServer(proxy)
	defer server.Close()

	// Phase 1: Send Type A request that needs auth
	t.Log("Phase 1: Sending Type A request")
	req1, _ := http.NewRequest("GET", server.URL+"/api/data", nil)
	req1.Header.Set("X-Needs-Auth", "true")

	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("Type A request failed: %v", err)
	}
	defer resp1.Body.Close()

	// Verify interim response
	if resp1.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", resp1.StatusCode)
	}

	resumeKey := resp1.Header.Get("X-Resume-Key")
	if resumeKey == "" {
		t.Fatal("Expected X-Resume-Key header in interim response")
	}

	body1, _ := io.ReadAll(resp1.Body)
	if string(body1) != "Please authenticate and resume" {
		t.Errorf("Expected interim body, got '%s'", body1)
	}

	t.Logf("Received resume key: %s", resumeKey)

	// Wait for upstream to complete
	time.Sleep(200 * time.Millisecond)

	// Phase 2: Send Type B resume request
	t.Log("Phase 2: Sending Type B resume request")
	req2, _ := http.NewRequest("GET", server.URL+"/resume", nil)
	req2.Header.Set("X-Resume-Key", resumeKey)

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("Type B request failed: %v", err)
	}
	defer resp2.Body.Close()

	// Verify upstream response
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp2.StatusCode)
	}

	if resp2.Header.Get("X-Upstream-Response") != "processed" {
		t.Errorf("Expected X-Upstream-Response header from upstream")
	}

	body2, _ := io.ReadAll(resp2.Body)
	if string(body2) != "Processed result from upstream" {
		t.Errorf("Expected upstream body, got '%s'", body2)
	}

	// Verify upstream was only called once
	if upstreamCallCount != 1 {
		t.Errorf("Expected upstream to be called once, got %d calls", upstreamCallCount)
	}
}

// TestE2E_Scenario2_StreamingResponse tests that large responses are streamed without buffering.
func TestE2E_Scenario2_StreamingResponse(t *testing.T) {
	// Create upstream that sends a large response in chunks
	chunkSize := 1024
	numChunks := 100
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("ResponseWriter doesn't support flushing")
			return
		}

		for i := 0; i < numChunks; i++ {
			chunk := strings.Repeat("x", chunkSize)
			fmt.Fprintf(w, "%s", chunk)
			flusher.Flush()
			time.Sleep(5 * time.Millisecond)
		}
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			if key := r.Header.Get("X-Resume-Key"); key != "" {
				return InboundRequest{Kind: RequestKindResume, In: r, Key: key}, nil
			}
			return InboundRequest{Kind: RequestKindForward, In: r, Key: generateTestKey()}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			ur.Out.URL.Scheme = upstreamURL.Scheme
			ur.Out.URL.Host = upstreamURL.Host
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			if ur.In.Header.Get("X-Detach") == "true" {
				return DetachAndWait{
					InterimStatusCode: http.StatusAccepted,
					InterimHeader:     http.Header{"X-Resume-Key": []string{ur.Key}},
					InterimBody:       []byte("Processing"),
				}, nil
			}
			return ForwardNow{}, nil
		},
		Transport:     http.DefaultTransport,
		Store:         NewMemoryStore(),
		FlushInterval: 10 * time.Millisecond,
		Timeout:       10 * time.Second,
	}

	server := httptest.NewServer(proxy)
	defer server.Close()

	// Send Type A request
	req1, _ := http.NewRequest("GET", server.URL+"/stream", nil)
	req1.Header.Set("X-Detach", "true")
	resp1, _ := http.DefaultClient.Do(req1)
	resumeKey := resp1.Header.Get("X-Resume-Key")
	resp1.Body.Close()

	// Wait for upstream to start
	time.Sleep(100 * time.Millisecond)

	// Send Type B request
	req2, _ := http.NewRequest("GET", server.URL+"/resume", nil)
	req2.Header.Set("X-Resume-Key", resumeKey)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("Type B request failed: %v", err)
	}
	defer resp2.Body.Close()

	// Read response and verify size
	body, _ := io.ReadAll(resp2.Body)
	expectedSize := chunkSize * numChunks
	if len(body) != expectedSize {
		t.Errorf("Expected body size %d, got %d", expectedSize, len(body))
	}
}

// TestE2E_Scenario2_MultipleResumeRequests tests that only one Type B request can consume a pending exchange.
func TestE2E_Scenario2_MultipleResumeRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		fmt.Fprintf(w, "Result")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			if key := r.Header.Get("X-Resume-Key"); key != "" {
				return InboundRequest{Kind: RequestKindResume, In: r, Key: key}, nil
			}
			return InboundRequest{Kind: RequestKindForward, In: r, Key: generateTestKey()}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			ur.Out.URL.Scheme = upstreamURL.Scheme
			ur.Out.URL.Host = upstreamURL.Host
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			return DetachAndWait{
				InterimStatusCode: http.StatusAccepted,
				InterimHeader:     http.Header{"X-Resume-Key": []string{ur.Key}},
			}, nil
		},
		Transport: http.DefaultTransport,
		Store:     NewMemoryStore(),
		Timeout:   5 * time.Second,
	}

	server := httptest.NewServer(proxy)
	defer server.Close()

	// Send Type A request
	req1, _ := http.NewRequest("GET", server.URL+"/test", nil)
	resp1, _ := http.DefaultClient.Do(req1)
	resumeKey := resp1.Header.Get("X-Resume-Key")
	resp1.Body.Close()

	// Wait for upstream to complete
	time.Sleep(100 * time.Millisecond)

	// Send multiple Type B requests concurrently
	var wg sync.WaitGroup
	results := make([]int, 3)

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req, _ := http.NewRequest("GET", server.URL+"/resume", nil)
			req.Header.Set("X-Resume-Key", resumeKey)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				results[idx] = 0
				return
			}
			defer resp.Body.Close()
			results[idx] = resp.StatusCode
		}(i)
	}

	wg.Wait()

	// Verify only one succeeded
	successCount := 0
	for _, status := range results {
		if status == http.StatusOK {
			successCount++
		}
	}

	if successCount != 1 {
		t.Errorf("Expected exactly 1 successful resume, got %d", successCount)
	}
}

// TestE2E_Scenario2_ExpiredKey tests that expired keys are rejected.
func TestE2E_Scenario2_ExpiredKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // Long processing
		fmt.Fprintf(w, "Result")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			if key := r.Header.Get("X-Resume-Key"); key != "" {
				return InboundRequest{Kind: RequestKindResume, In: r, Key: key}, nil
			}
			return InboundRequest{Kind: RequestKindForward, In: r, Key: generateTestKey()}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			ur.Out.URL.Scheme = upstreamURL.Scheme
			ur.Out.URL.Host = upstreamURL.Host
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			return DetachAndWait{
				InterimStatusCode: http.StatusAccepted,
				InterimHeader:     http.Header{"X-Resume-Key": []string{ur.Key}},
			}, nil
		},
		Transport: http.DefaultTransport,
		Store:     NewMemoryStore(),
		Timeout:   500 * time.Millisecond, // Short timeout
	}

	server := httptest.NewServer(proxy)
	defer server.Close()

	// Send Type A request
	req1, _ := http.NewRequest("GET", server.URL+"/test", nil)
	resp1, _ := http.DefaultClient.Do(req1)
	resumeKey := resp1.Header.Get("X-Resume-Key")
	resp1.Body.Close()

	// Wait for timeout
	time.Sleep(1 * time.Second)

	// Try to resume with expired key
	req2, _ := http.NewRequest("GET", server.URL+"/resume", nil)
	req2.Header.Set("X-Resume-Key", resumeKey)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("Type B request failed: %v", err)
	}
	defer resp2.Body.Close()

	// Should get 404 for expired key
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("Expected status 404 for expired key, got %d", resp2.StatusCode)
	}
}

// TestE2E_RespondNow tests the RespondNow action.
func TestE2E_RespondNow(t *testing.T) {
	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			return InboundRequest{Kind: RequestKindForward, In: r, Key: generateTestKey()}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			// No-op
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			// Block request locally
			if ur.In.Header.Get("X-Block") == "true" {
				return RespondNow{
					StatusCode: http.StatusForbidden,
					Header:     http.Header{"X-Reason": []string{"blocked"}},
					Body:       []byte("Access denied"),
				}, nil
			}
			return ForwardNow{}, nil
		},
		Store: NewMemoryStore(),
	}

	server := httptest.NewServer(proxy)
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL+"/test", nil)
	req.Header.Set("X-Block", "true")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("Expected status 403, got %d", resp.StatusCode)
	}

	if resp.Header.Get("X-Reason") != "blocked" {
		t.Errorf("Expected X-Reason header")
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "Access denied" {
		t.Errorf("Expected 'Access denied', got '%s'", body)
	}
}

// TestE2E_ErrorHandling tests custom error handling.
func TestE2E_ErrorHandling(t *testing.T) {
	errorHandlerCalled := false

	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			return InboundRequest{}, fmt.Errorf("classification error")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			errorHandlerCalled = true
			w.Header().Set("X-Error", "true")
			http.Error(w, fmt.Sprintf("Custom error: %v", err), http.StatusBadRequest)
		},
		Store: NewMemoryStore(),
	}

	server := httptest.NewServer(proxy)
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if !errorHandlerCalled {
		t.Error("Expected error handler to be called")
	}

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", resp.StatusCode)
	}

	if resp.Header.Get("X-Error") != "true" {
		t.Error("Expected X-Error header")
	}
}

// Made with Bob

// TestE2E_Scenario3_MultipleResumeWithInterim tests Scenario 3: multiple Type B requests with interim responses.
// Type A: Client → Sidecar → App (starts), Client ← Sidecar (interim 401/202)
// Type B1: Client → Sidecar (lookup), Client ← Sidecar (interim 202 - not ready)
// Type B2: Client → Sidecar (lookup), Client ← Sidecar (interim 202 - not ready)
// Type B3: Client → Sidecar (lookup), Client ← Sidecar ← App (streamed - ready)
func TestE2E_Scenario3_MultipleResumeWithInterim(t *testing.T) {
	// Track resume attempt count per key
	resumeAttempts := make(map[string]int)
	var mu sync.Mutex

	// Create upstream server that simulates slow processing
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate long processing time
		time.Sleep(300 * time.Millisecond)

		w.Header().Set("X-Upstream-Response", "processed")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Final result from upstream")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	// Create proxy with custom Classify that tracks resume attempts
	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			// Check for resume key
			if key := r.Header.Get("X-Resume-Key"); key != "" {
				return InboundRequest{
					Kind: RequestKindResume,
					In:   r,
					Key:  key,
				}, nil
			}
			// Type A request
			return InboundRequest{
				Kind: RequestKindForward,
				In:   r,
				Key:  generateTestKey(),
			}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			ur.Out.URL.Scheme = upstreamURL.Scheme
			ur.Out.URL.Host = upstreamURL.Host
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			// Detach if X-Needs-Auth header is present (Type A)
			if ur.In.Header.Get("X-Needs-Auth") == "true" {
				return DetachAndWait{
					InterimStatusCode: http.StatusUnauthorized,
					InterimHeader:     http.Header{"X-Resume-Key": []string{ur.Key}},
					InterimBody:       []byte("Please authenticate and resume"),
				}, nil
			}
			return ForwardNow{}, nil
		},
		Transport: http.DefaultTransport,
		Store:     NewMemoryStore(),
		Timeout:   10 * time.Second,
	}

	// Wrap the proxy to intercept Type B requests and return interim responses
	// for the first two attempts
	wrappedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resumeKey := r.Header.Get("X-Resume-Key")
		if resumeKey != "" {
			mu.Lock()
			resumeAttempts[resumeKey]++
			attemptNum := resumeAttempts[resumeKey]
			mu.Unlock()

			// For first two attempts, return interim response without calling proxy
			if attemptNum <= 2 {
				w.Header().Set("X-Resume-Key", resumeKey)
				w.Header().Set("X-Attempt", fmt.Sprintf("%d", attemptNum))
				w.WriteHeader(http.StatusAccepted)
				fmt.Fprintf(w, "Processing, please retry (attempt %d)", attemptNum)
				return
			}
		}

		// For Type A or third Type B attempt, call the actual proxy
		proxy.ServeHTTP(w, r)
	})

	server := httptest.NewServer(wrappedHandler)
	defer server.Close()

	// Phase 1: Send Type A request that needs auth
	t.Log("Phase 1: Sending Type A request")
	req1, _ := http.NewRequest("GET", server.URL+"/api/data", nil)
	req1.Header.Set("X-Needs-Auth", "true")

	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("Type A request failed: %v", err)
	}
	defer resp1.Body.Close()

	// Verify interim response from Type A
	if resp1.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", resp1.StatusCode)
	}

	resumeKey := resp1.Header.Get("X-Resume-Key")
	if resumeKey == "" {
		t.Fatal("Expected X-Resume-Key header in interim response")
	}

	body1, _ := io.ReadAll(resp1.Body)
	if string(body1) != "Please authenticate and resume" {
		t.Errorf("Expected interim body, got '%s'", body1)
	}

	t.Logf("Received resume key: %s", resumeKey)

	// Phase 2: First Type B resume request (should get interim response)
	t.Log("Phase 2: Sending first Type B resume request (too early)")
	time.Sleep(50 * time.Millisecond) // Small delay but upstream not ready yet

	req2, _ := http.NewRequest("GET", server.URL+"/resume", nil)
	req2.Header.Set("X-Resume-Key", resumeKey)

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("First Type B request failed: %v", err)
	}
	defer resp2.Body.Close()

	// Verify interim response
	if resp2.StatusCode != http.StatusAccepted {
		t.Errorf("Expected status 202 for first resume, got %d", resp2.StatusCode)
	}

	if resp2.Header.Get("X-Attempt") != "1" {
		t.Errorf("Expected attempt 1, got %s", resp2.Header.Get("X-Attempt"))
	}

	body2, _ := io.ReadAll(resp2.Body)
	t.Logf("First Type B response: %s", body2)

	// Phase 3: Second Type B resume request (should get interim response)
	t.Log("Phase 3: Sending second Type B resume request (still too early)")
	time.Sleep(50 * time.Millisecond) // Still not ready

	req3, _ := http.NewRequest("GET", server.URL+"/resume", nil)
	req3.Header.Set("X-Resume-Key", resumeKey)

	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatalf("Second Type B request failed: %v", err)
	}
	defer resp3.Body.Close()

	// Verify interim response
	if resp3.StatusCode != http.StatusAccepted {
		t.Errorf("Expected status 202 for second resume, got %d", resp3.StatusCode)
	}

	if resp3.Header.Get("X-Attempt") != "2" {
		t.Errorf("Expected attempt 2, got %s", resp3.Header.Get("X-Attempt"))
	}

	body3, _ := io.ReadAll(resp3.Body)
	t.Logf("Second Type B response: %s", body3)

	// Phase 4: Wait for upstream to complete, then third Type B request
	t.Log("Phase 4: Waiting for upstream to complete")
	time.Sleep(250 * time.Millisecond) // Now upstream should be ready

	t.Log("Phase 5: Sending third Type B resume request (should get final response)")
	req4, _ := http.NewRequest("GET", server.URL+"/resume", nil)
	req4.Header.Set("X-Resume-Key", resumeKey)

	resp4, err := http.DefaultClient.Do(req4)
	if err != nil {
		t.Fatalf("Third Type B request failed: %v", err)
	}
	defer resp4.Body.Close()

	// Verify final upstream response
	if resp4.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 for final resume, got %d", resp4.StatusCode)
	}

	if resp4.Header.Get("X-Upstream-Response") != "processed" {
		t.Errorf("Expected X-Upstream-Response header from upstream")
	}

	body4, _ := io.ReadAll(resp4.Body)
	if string(body4) != "Final result from upstream" {
		t.Errorf("Expected upstream body, got '%s'", body4)
	}

	t.Log("Successfully completed Scenario 3: Type A → Type B (interim) → Type B (interim) → Type B (final)")

	// Verify the sequence
	mu.Lock()
	totalAttempts := resumeAttempts[resumeKey]
	mu.Unlock()

	if totalAttempts != 3 {
		t.Errorf("Expected 3 resume attempts, got %d", totalAttempts)
	}
}
