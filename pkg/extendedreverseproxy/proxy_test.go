// Copyright 2026 IBM. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package extendedreverseproxy

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// generateKey generates a random correlation key for testing.
func generateKey() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// TestScenario1_NormalForward tests normal forwarding without detachment.
func TestScenario1_NormalForward(t *testing.T) {
	// Create upstream server
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "true")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("upstream response"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	// Create proxy
	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			return InboundRequest{
				Kind: RequestKindForward,
				In:   r,
				Key:  generateKey(),
			}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			ur.Out.URL.Scheme = upstreamURL.Scheme
			ur.Out.URL.Host = upstreamURL.Host
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			return ForwardNow{}, nil
		},
		Transport: http.DefaultTransport,
		Store:     NewMemoryStore(),
	}

	// Test request
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	// Verify response
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	if rec.Body.String() != "upstream response" {
		t.Errorf("expected 'upstream response', got %q", rec.Body.String())
	}
	if rec.Header().Get("X-Upstream") != "true" {
		t.Errorf("expected X-Upstream header")
	}
}

// TestScenario2_DetachAndResume tests detached flow with resume.
func TestScenario2_DetachAndResume(t *testing.T) {
	// Create upstream server with delay
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond) // Simulate processing
		w.Header().Set("X-Upstream", "true")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("upstream response"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	// Create proxy
	store := NewMemoryStore()
	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			if key := r.Header.Get("X-Resume-Key"); key != "" {
				return InboundRequest{
					Kind: RequestKindResume,
					In:   r,
					Key:  key,
				}, nil
			}
			return InboundRequest{
				Kind: RequestKindForward,
				In:   r,
				Key:  generateKey(),
			}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			ur.Out.URL.Scheme = upstreamURL.Scheme
			ur.Out.URL.Host = upstreamURL.Host
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			// Detach if X-Detach header is present
			if ur.In.Header.Get("X-Detach") == "true" {
				return DetachAndWait{
					InterimStatusCode: http.StatusUnauthorized,
					InterimHeader:     http.Header{"X-Resume-Key": []string{ur.Key}},
					InterimBody:       []byte("auth required"),
				}, nil
			}
			return ForwardNow{}, nil
		},
		Transport: http.DefaultTransport,
		Store:     store,
		Timeout:   5 * time.Second,
	}

	// Phase 1: Send Type A request with detach
	reqA := httptest.NewRequest("GET", "/test", nil)
	reqA.Header.Set("X-Detach", "true")
	recA := httptest.NewRecorder()

	proxy.ServeHTTP(recA, reqA)

	// Verify interim response
	if recA.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", recA.Code)
	}
	if recA.Body.String() != "auth required" {
		t.Errorf("expected 'auth required', got %q", recA.Body.String())
	}

	resumeKey := recA.Header().Get("X-Resume-Key")
	if resumeKey == "" {
		t.Fatal("expected X-Resume-Key header")
	}

	// Wait a bit for upstream to complete
	time.Sleep(200 * time.Millisecond)

	// Phase 2: Send Type B resume request
	reqB := httptest.NewRequest("GET", "/resume", nil)
	reqB.Header.Set("X-Resume-Key", resumeKey)
	recB := httptest.NewRecorder()

	proxy.ServeHTTP(recB, reqB)

	// Verify final response
	if recB.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", recB.Code)
	}
	if recB.Body.String() != "upstream response" {
		t.Errorf("expected 'upstream response', got %q", recB.Body.String())
	}
	if recB.Header().Get("X-Upstream") != "true" {
		t.Errorf("expected X-Upstream header")
	}
}

func TestScenario2_ResumeFromNewClientConnection(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("upstream response"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			if key := r.Header.Get("X-Resume-Key"); key != "" {
				return InboundRequest{
					Kind: RequestKindResume,
					In:   r,
					Key:  key,
				}, nil
			}
			return InboundRequest{
				Kind: RequestKindForward,
				In:   r,
				Key:  generateKey(),
			}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			ur.Out.URL.Scheme = upstreamURL.Scheme
			ur.Out.URL.Host = upstreamURL.Host
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			return DetachAndWait{
				InterimStatusCode: http.StatusAccepted,
				InterimHeader:     http.Header{"X-Resume-Key": []string{ur.Key}},
				InterimBody:       []byte("pending"),
			}, nil
		},
		Transport: http.DefaultTransport,
		Store:     NewMemoryStore(),
		Timeout:   5 * time.Second,
	}

	server := httptest.NewServer(proxy)
	defer server.Close()

	clientA := &http.Client{}
	reqA, _ := http.NewRequest(http.MethodGet, server.URL+"/test", nil)
	respA, err := clientA.Do(reqA)
	if err != nil {
		t.Fatalf("initial request failed: %v", err)
	}
	resumeKey := respA.Header.Get("X-Resume-Key")
	respA.Body.Close()

	if resumeKey == "" {
		t.Fatal("expected X-Resume-Key header")
	}

	time.Sleep(200 * time.Millisecond)

	clientB := &http.Client{}
	reqB, _ := http.NewRequest(http.MethodGet, server.URL+"/resume", nil)
	reqB.Header.Set("X-Resume-Key", resumeKey)
	respB, err := clientB.Do(reqB)
	if err != nil {
		t.Fatalf("resume request failed: %v", err)
	}
	defer respB.Body.Close()

	if respB.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", respB.StatusCode)
	}
}

// TestScenario2_ResumeBeforeUpstreamReady tests resume request arriving before upstream completes.
func TestScenario2_ResumeBeforeUpstreamReady(t *testing.T) {
	// Create upstream server with significant delay
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("delayed response"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	store := NewMemoryStore()
	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			if key := r.Header.Get("X-Resume-Key"); key != "" {
				return InboundRequest{
					Kind: RequestKindResume,
					In:   r,
					Key:  key,
				}, nil
			}
			return InboundRequest{
				Kind: RequestKindForward,
				In:   r,
				Key:  generateKey(),
			}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			ur.Out.URL.Scheme = upstreamURL.Scheme
			ur.Out.URL.Host = upstreamURL.Host
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			return DetachAndWait{
				InterimStatusCode: http.StatusAccepted,
				InterimHeader:     http.Header{"X-Resume-Key": []string{ur.Key}},
				InterimBody:       []byte("processing"),
			}, nil
		},
		Transport: http.DefaultTransport,
		Store:     store,
		Timeout:   5 * time.Second,
	}

	// Send Type A request
	reqA := httptest.NewRequest("GET", "/test", nil)
	recA := httptest.NewRecorder()
	proxy.ServeHTTP(recA, reqA)

	resumeKey := recA.Header().Get("X-Resume-Key")
	if resumeKey == "" {
		t.Fatal("expected X-Resume-Key header")
	}

	// Immediately send Type B request (before upstream completes)
	reqB := httptest.NewRequest("GET", "/resume", nil)
	reqB.Header.Set("X-Resume-Key", resumeKey)
	recB := httptest.NewRecorder()

	// This should block until upstream completes
	proxy.ServeHTTP(recB, reqB)

	// Verify we got the upstream response
	if recB.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", recB.Code)
	}
	if recB.Body.String() != "delayed response" {
		t.Errorf("expected 'delayed response', got %q", recB.Body.String())
	}
}

func TestScenario2_ResumeOnSecondConnectionWhileFirstConnectionRemainsOpen(t *testing.T) {
	upstreamStarted := make(chan struct{}, 1)
	releaseUpstream := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamStarted <- struct{}{}
		<-releaseUpstream
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("second connection resumed response"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			if key := r.Header.Get("X-Resume-Key"); key != "" {
				return InboundRequest{
					Kind: RequestKindResume,
					In:   r,
					Key:  key,
				}, nil
			}
			return InboundRequest{
				Kind: RequestKindForward,
				In:   r,
				Key:  generateKey(),
			}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			ur.Out.URL.Scheme = upstreamURL.Scheme
			ur.Out.URL.Host = upstreamURL.Host
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			return DetachAndWait{
				InterimStatusCode: http.StatusAccepted,
				InterimHeader:     http.Header{"X-Resume-Key": []string{ur.Key}},
				InterimBody:       []byte("pending"),
			}, nil
		},
		Transport: http.DefaultTransport,
		Store:     NewMemoryStore(),
		Timeout:   5 * time.Second,
	}

	server := httptest.NewServer(proxy)
	defer server.Close()

	client1 := &http.Client{}
	req1, _ := http.NewRequest(http.MethodGet, server.URL+"/test", nil)
	resp1, err := client1.Do(req1)
	if err != nil {
		t.Fatalf("initial request failed: %v", err)
	}
	defer resp1.Body.Close()

	if resp1.StatusCode != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", resp1.StatusCode)
	}

	resumeKey := resp1.Header.Get("X-Resume-Key")
	if resumeKey == "" {
		t.Fatal("expected X-Resume-Key header")
	}

	select {
	case <-upstreamStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for detached upstream request to start")
	}

	resultCh := make(chan *http.Response, 1)
	errCh := make(chan error, 1)

	go func() {
		client2 := &http.Client{}
		req2, _ := http.NewRequest(http.MethodGet, server.URL+"/resume", nil)
		req2.Header.Set("X-Resume-Key", resumeKey)
		resp2, err := client2.Do(req2)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- resp2
	}()

	time.Sleep(100 * time.Millisecond)
	close(releaseUpstream)

	select {
	case err := <-errCh:
		t.Fatalf("resume request failed: %v", err)
	case resp2 := <-resultCh:
		defer resp2.Body.Close()
		if resp2.StatusCode != http.StatusOK {
			t.Fatalf("expected status 200, got %d", resp2.StatusCode)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for resume response on second connection")
	}
}

// TestResumeKeyNotFound tests resume with unknown key.
func TestResumeKeyNotFound(t *testing.T) {
	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			return InboundRequest{
				Kind: RequestKindResume,
				In:   r,
				Key:  "unknown-key",
			}, nil
		},
		Store: NewMemoryStore(),
	}

	req := httptest.NewRequest("GET", "/resume", nil)
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}
}

// TestRespondNow tests local response without upstream.
func TestRespondNow(t *testing.T) {
	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			return InboundRequest{
				Kind: RequestKindForward,
				In:   r,
				Key:  generateKey(),
			}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			// No-op
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			return RespondNow{
				StatusCode: http.StatusForbidden,
				Header:     http.Header{"X-Local": []string{"true"}},
				Body:       []byte("access denied"),
			}, nil
		},
		Store: NewMemoryStore(),
	}

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", rec.Code)
	}
	if rec.Body.String() != "access denied" {
		t.Errorf("expected 'access denied', got %q", rec.Body.String())
	}
	if rec.Header().Get("X-Local") != "true" {
		t.Errorf("expected X-Local header")
	}
}

// TestModifyResponse tests response modification hook.
func TestModifyResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("original"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			return InboundRequest{
				Kind: RequestKindForward,
				In:   r,
				Key:  generateKey(),
			}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			ur.Out.URL.Scheme = upstreamURL.Scheme
			ur.Out.URL.Host = upstreamURL.Host
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			return ForwardNow{}, nil
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Set("X-Modified", "true")
			return nil
		},
		Transport: http.DefaultTransport,
		Store:     NewMemoryStore(),
	}

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	if rec.Header().Get("X-Modified") != "true" {
		t.Errorf("expected X-Modified header")
	}
}

// TestStreamingResponse tests streaming with large response.
func TestStreamingResponse(t *testing.T) {
	// Create upstream that sends data in chunks
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}

		w.WriteHeader(http.StatusOK)
		for i := 0; i < 5; i++ {
			fmt.Fprintf(w, "chunk%d\n", i)
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			return InboundRequest{
				Kind: RequestKindForward,
				In:   r,
				Key:  generateKey(),
			}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			ur.Out.URL.Scheme = upstreamURL.Scheme
			ur.Out.URL.Host = upstreamURL.Host
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			return ForwardNow{}, nil
		},
		Transport:     http.DefaultTransport,
		FlushInterval: 10 * time.Millisecond,
		Store:         NewMemoryStore(),
	}

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	body := rec.Body.String()
	expected := "chunk0\nchunk1\nchunk2\nchunk3\nchunk4\n"
	if body != expected {
		t.Errorf("expected %q, got %q", expected, body)
	}
}

// TestErrorHandler tests custom error handling.
func TestErrorHandler(t *testing.T) {
	errorHandlerCalled := false

	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			return InboundRequest{}, fmt.Errorf("classification error")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			errorHandlerCalled = true
			http.Error(w, "custom error", http.StatusTeapot)
		},
		Store: NewMemoryStore(),
	}

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	if !errorHandlerCalled {
		t.Error("expected error handler to be called")
	}
	if rec.Code != http.StatusTeapot {
		t.Errorf("expected status 418, got %d", rec.Code)
	}
}

// TestUpstreamFailure tests handling of upstream errors.
func TestUpstreamFailure(t *testing.T) {
	// Create upstream that fails
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Close connection without response
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			conn.Close()
		}
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			return InboundRequest{
				Kind: RequestKindForward,
				In:   r,
				Key:  generateKey(),
			}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			ur.Out.URL.Scheme = upstreamURL.Scheme
			ur.Out.URL.Host = upstreamURL.Host
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			return ForwardNow{}, nil
		},
		Transport: http.DefaultTransport,
		Store:     NewMemoryStore(),
	}

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	// Should get error response
	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected status 502, got %d", rec.Code)
	}
}

// TestHopByHopHeaders tests that hop-by-hop headers are removed.
func TestHopByHopHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check that hop-by-hop headers were removed from request
		if r.Header.Get("Connection") != "" {
			t.Error("Connection header should be removed")
		}
		if r.Header.Get("Keep-Alive") != "" {
			t.Error("Keep-Alive header should be removed")
		}

		// Send response with hop-by-hop headers
		w.Header().Set("Connection", "close")
		w.Header().Set("Keep-Alive", "timeout=5")
		w.Header().Set("X-Custom", "value")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			return InboundRequest{
				Kind: RequestKindForward,
				In:   r,
				Key:  generateKey(),
			}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			ur.Out.URL.Scheme = upstreamURL.Scheme
			ur.Out.URL.Host = upstreamURL.Host
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			return ForwardNow{}, nil
		},
		Transport: http.DefaultTransport,
		Store:     NewMemoryStore(),
	}

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Keep-Alive", "timeout=10")
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	// Check that hop-by-hop headers were removed from response
	if rec.Header().Get("Connection") != "" {
		t.Error("Connection header should be removed from response")
	}
	if rec.Header().Get("Keep-Alive") != "" {
		t.Error("Keep-Alive header should be removed from response")
	}
	// But custom headers should remain
	if rec.Header().Get("X-Custom") != "value" {
		t.Error("X-Custom header should be preserved")
	}
}

// TestForwardWithDetachOption_DynamicDetach tests ForwardWithDetachOption with dynamic detach signal.
func TestForwardWithDetachOption_DynamicDetach(t *testing.T) {
	// Create upstream server with delay to simulate processing
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("X-Upstream", "completed")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("upstream response after OAuth"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	store := NewMemoryStore()
	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			if key := r.Header.Get("X-Resume-Key"); key != "" {
				return InboundRequest{
					Kind: RequestKindResume,
					In:   r,
					Key:  key,
				}, nil
			}
			return InboundRequest{
				Kind: RequestKindForward,
				In:   r,
				Key:  generateKey(),
			}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			ur.Out.URL.Scheme = upstreamURL.Scheme
			ur.Out.URL.Host = upstreamURL.Host
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			// Use ForwardWithDetachOption to simulate OAuth discovery
			if ur.In.Header.Get("X-OAuth-Flow") == "true" {
				detachSignal := make(chan DetachAndWait, 1)

				// Simulate OAuth discovery after a delay
				go func() {
					time.Sleep(50 * time.Millisecond)
					detachSignal <- DetachAndWait{
						InterimStatusCode: http.StatusUnauthorized,
						InterimHeader:     http.Header{"X-Resume-Key": []string{ur.Key}},
						InterimBody:       []byte("OAuth required"),
					}
				}()

				return ForwardWithDetachOption{
					DetachSignal: detachSignal,
				}, nil
			}
			return ForwardNow{}, nil
		},
		Transport: http.DefaultTransport,
		Store:     store,
		Timeout:   5 * time.Second,
	}

	// Phase 1: Send request that will trigger dynamic detach
	reqA := httptest.NewRequest("GET", "/test", nil)
	reqA.Header.Set("X-OAuth-Flow", "true")
	recA := httptest.NewRecorder()

	proxy.ServeHTTP(recA, reqA)

	// Verify interim response
	if recA.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", recA.Code)
	}
	if recA.Body.String() != "OAuth required" {
		t.Errorf("expected 'OAuth required', got %q", recA.Body.String())
	}

	resumeKey := recA.Header().Get("X-Resume-Key")
	if resumeKey == "" {
		t.Fatal("expected X-Resume-Key header")
	}

	// Wait for upstream to complete
	time.Sleep(300 * time.Millisecond)

	// Phase 2: Send resume request
	reqB := httptest.NewRequest("GET", "/resume", nil)
	reqB.Header.Set("X-Resume-Key", resumeKey)
	recB := httptest.NewRecorder()

	proxy.ServeHTTP(recB, reqB)

	// Verify we got the upstream response
	if recB.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", recB.Code)
	}
	if recB.Body.String() != "upstream response after OAuth" {
		t.Errorf("expected 'upstream response after OAuth', got %q", recB.Body.String())
	}
	if recB.Header().Get("X-Upstream") != "completed" {
		t.Errorf("expected X-Upstream header from upstream")
	}
}

// TestForwardWithDetachOption_NoDetach tests ForwardWithDetachOption when no detach signal is sent.
func TestForwardWithDetachOption_NoDetach(t *testing.T) {
	// Create upstream server
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "direct")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("direct response"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			return InboundRequest{
				Kind: RequestKindForward,
				In:   r,
				Key:  generateKey(),
			}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			ur.Out.URL.Scheme = upstreamURL.Scheme
			ur.Out.URL.Host = upstreamURL.Host
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			// Create detach signal but never send to it
			detachSignal := make(chan DetachAndWait, 1)
			return ForwardWithDetachOption{
				DetachSignal: detachSignal,
			}, nil
		},
		Transport: http.DefaultTransport,
		Store:     NewMemoryStore(),
		Timeout:   5 * time.Second,
	}

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	// Should get direct response without detach
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	if rec.Body.String() != "direct response" {
		t.Errorf("expected 'direct response', got %q", rec.Body.String())
	}
	if rec.Header().Get("X-Upstream") != "direct" {
		t.Errorf("expected X-Upstream header")
	}
}

// TestForwardWithDetachOption_ClientDisconnect tests that upstream continues after client disconnect.
func TestForwardWithDetachOption_ClientDisconnect(t *testing.T) {
	upstreamStarted := make(chan struct{})
	upstreamCompleted := make(chan struct{})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(upstreamStarted)
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("upstream completed"))
		close(upstreamCompleted)
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	store := NewMemoryStore()
	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			if key := r.Header.Get("X-Resume-Key"); key != "" {
				return InboundRequest{
					Kind: RequestKindResume,
					In:   r,
					Key:  key,
				}, nil
			}
			return InboundRequest{
				Kind: RequestKindForward,
				In:   r,
				Key:  generateKey(),
			}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			ur.Out.URL.Scheme = upstreamURL.Scheme
			ur.Out.URL.Host = upstreamURL.Host
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			detachSignal := make(chan DetachAndWait, 1)

			go func() {
				<-upstreamStarted
				time.Sleep(50 * time.Millisecond)
				detachSignal <- DetachAndWait{
					InterimStatusCode: http.StatusUnauthorized,
					InterimHeader:     http.Header{"X-Resume-Key": []string{ur.Key}},
					InterimBody:       []byte("auth required"),
				}
			}()

			return ForwardWithDetachOption{
				DetachSignal: detachSignal,
			}, nil
		},
		Transport: http.DefaultTransport,
		Store:     store,
		Timeout:   5 * time.Second,
	}

	server := httptest.NewServer(proxy)
	defer server.Close()

	// Phase 1: Send request and get interim response
	client := &http.Client{}
	req1, _ := http.NewRequest("GET", server.URL+"/test", nil)
	resp1, err := client.Do(req1)
	if err != nil {
		t.Fatalf("initial request failed: %v", err)
	}

	resumeKey := resp1.Header.Get("X-Resume-Key")
	resp1.Body.Close() // Client disconnects

	if resumeKey == "" {
		t.Fatal("expected X-Resume-Key header")
	}

	// Wait for upstream to complete despite client disconnect
	select {
	case <-upstreamCompleted:
		t.Log("Upstream completed successfully after client disconnect")
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not complete after client disconnect")
	}

	// Phase 2: Resume from new client connection
	req2, _ := http.NewRequest("GET", server.URL+"/resume", nil)
	req2.Header.Set("X-Resume-Key", resumeKey)
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("resume request failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp2.StatusCode)
	}
}

// TestForwardWithDetachOption_EarlyDetach tests detach signal arriving before upstream starts.
func TestForwardWithDetachOption_EarlyDetach(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("upstream response"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	store := NewMemoryStore()
	proxy := &ExtendedReverseProxy{
		Classify: func(r *http.Request) (InboundRequest, error) {
			if key := r.Header.Get("X-Resume-Key"); key != "" {
				return InboundRequest{
					Kind: RequestKindResume,
					In:   r,
					Key:  key,
				}, nil
			}
			return InboundRequest{
				Kind: RequestKindForward,
				In:   r,
				Key:  generateKey(),
			}, nil
		},
		Rewrite: func(ur *UpstreamRequest) {
			ur.Out.URL.Scheme = upstreamURL.Scheme
			ur.Out.URL.Host = upstreamURL.Host
		},
		Decide: func(ur *UpstreamRequest) (ProxyAction, error) {
			detachSignal := make(chan DetachAndWait, 1)

			// Send detach signal immediately (before upstream even starts)
			detachSignal <- DetachAndWait{
				InterimStatusCode: http.StatusUnauthorized,
				InterimHeader:     http.Header{"X-Resume-Key": []string{ur.Key}},
				InterimBody:       []byte("immediate auth"),
			}

			return ForwardWithDetachOption{
				DetachSignal: detachSignal,
			}, nil
		},
		Transport: http.DefaultTransport,
		Store:     store,
		Timeout:   5 * time.Second,
	}

	// Send request
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	// Should get interim response immediately
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rec.Code)
	}

	resumeKey := rec.Header().Get("X-Resume-Key")
	if resumeKey == "" {
		t.Fatal("expected X-Resume-Key header")
	}

	// Upstream should still be called and complete
	time.Sleep(200 * time.Millisecond)

	if !upstreamCalled {
		t.Error("expected upstream to be called despite early detach")
	}

	// Resume should work
	reqB := httptest.NewRequest("GET", "/resume", nil)
	reqB.Header.Set("X-Resume-Key", resumeKey)
	recB := httptest.NewRecorder()

	proxy.ServeHTTP(recB, reqB)

	if recB.Code != http.StatusOK {
		t.Errorf("expected status 200 on resume, got %d", recB.Code)
	}
	if recB.Body.String() != "upstream response" {
		t.Errorf("expected 'upstream response', got %q", recB.Body.String())
	}
}

// Made with Bob
