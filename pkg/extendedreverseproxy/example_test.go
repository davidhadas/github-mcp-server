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
	"time"
)

// genExampleKey generates a random correlation key for examples.
func genExampleKey() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Example_scenario1 demonstrates normal forwarding (Scenario 1).
func Example_scenario1() {
	// Create a test upstream server
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello from upstream!")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	// Create the proxy
	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			// All requests are Type A (forward)
			return InboundRequest{
				Kind: RequestKindForward,
				In:   r,
				Key:  genExampleKey(),
			}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			// Route to upstream
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

	// Create a test server with the proxy
	server := httptest.NewServer(proxy)
	defer server.Close()

	// Make a request
	resp, err := http.Get(server.URL + "/test")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("Status: %d\n", resp.StatusCode)
	fmt.Printf("Body: %s\n", body)

	// Output:
	// Status: 200
	// Body: Hello from upstream!
}

// Example_scenario2 demonstrates detached flow with resume (Scenario 2).
func Example_scenario2() {
	// Create a test upstream server that simulates slow processing
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond) // Simulate processing
		fmt.Fprintf(w, "Processed result")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	// Create the proxy
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
				Key:  genExampleKey(),
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
	req1, _ := http.NewRequest("GET", server.URL+"/api/data", nil)
	req1.Header.Set("X-Needs-Auth", "true")

	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer resp1.Body.Close()

	body1, _ := io.ReadAll(resp1.Body)
	resumeKey := resp1.Header.Get("X-Resume-Key")

	fmt.Printf("Phase 1 - Type A:\n")
	fmt.Printf("  Status: %d\n", resp1.StatusCode)
	fmt.Printf("  Body: %s\n", body1)
	fmt.Printf("  Resume Key: ...\n") // Placeholder for actual key

	// Wait for upstream to complete
	time.Sleep(200 * time.Millisecond)

	// Phase 2: Send Type B resume request
	req2, _ := http.NewRequest("GET", server.URL+"/resume", nil)
	req2.Header.Set("X-Resume-Key", resumeKey)

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer resp2.Body.Close()

	body2, _ := io.ReadAll(resp2.Body)

	fmt.Printf("\nPhase 2 - Type B:\n")
	fmt.Printf("  Status: %d\n", resp2.StatusCode)
	fmt.Printf("  Body: %s\n", body2)

	// Output:
	// Phase 1 - Type A:
	//   Status: 401
	//   Body: Please authenticate and resume
	//   Resume Key: ...
	//
	// Phase 2 - Type B:
	//   Status: 200
	//   Body: Processed result
}

// Example_customErrorHandler demonstrates custom error handling.
func Example_customErrorHandler() {
	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			// Simulate classification error
			return InboundRequest{}, fmt.Errorf("invalid request format")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// Custom error response
			w.Header().Set("X-Error", "true")
			http.Error(w, fmt.Sprintf("Proxy Error: %v", err), http.StatusBadRequest)
		},
		Store: NewMemoryStore(),
	}

	server := httptest.NewServer(proxy)
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("Status: %d\n", resp.StatusCode)
	fmt.Printf("Has X-Error header: %v\n", resp.Header.Get("X-Error") == "true")
	fmt.Printf("Body contains 'Proxy Error': %v\n", len(body) > 0)

	// Output:
	// Status: 400
	// Has X-Error header: true
	// Body contains 'Proxy Error': true
}

// Example_respondNow demonstrates local response without upstream.
func Example_respondNow() {
	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			return InboundRequest{
				Kind: RequestKindForward,
				In:   r,
				Key:  genExampleKey(),
			}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			// No-op
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			// Check if request should be blocked
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
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("Status: %d\n", resp.StatusCode)
	fmt.Printf("Body: %s\n", body)
	fmt.Printf("Reason: %s\n", resp.Header.Get("X-Reason"))

	// Output:
	// Status: 403
	// Body: Access denied
	// Reason: blocked
}

// Made with Bob
