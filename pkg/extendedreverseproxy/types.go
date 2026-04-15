// Copyright 2026 IBM. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package extendedreverseproxy

import (
	"context"
	"net/http"
	"time"
)

// RequestKind identifies the type of inbound request.
type RequestKind int

const (
	// RequestKindForward indicates a normal request to be proxied (Type A).
	RequestKindForward RequestKind = iota

	// RequestKindResume indicates a resume request to retrieve a pending response (Type B).
	RequestKindResume
)

// InboundRequest represents a classified incoming request.
type InboundRequest struct {
	Kind RequestKind
	In   *http.Request
	Key  string // Correlation key provided by caller
}

// UpstreamRequest represents a request being prepared for upstream.
type UpstreamRequest struct {
	In  *http.Request // Original inbound request
	Out *http.Request // Prepared upstream request
	Key string        // Correlation key for this exchange
}

// ProxyAction represents a decision about how to handle a request.
type ProxyAction interface {
	isProxyAction()
}

// ForwardNow streams the upstream response directly to the current request.
type ForwardNow struct{}

func (ForwardNow) isProxyAction() {}

// RespondNow sends a local response without waiting for upstream.
type RespondNow struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

func (RespondNow) isProxyAction() {}

// DetachAndWait sends an interim response and keeps the upstream request alive.
type DetachAndWait struct {
	InterimStatusCode int
	InterimHeader     http.Header
	InterimBody       []byte
}

func (DetachAndWait) isProxyAction() {}

// ForwardWithDetachOption forwards normally but can be interrupted to detach.
// If DetachSignal channel receives a DetachAndWait action, it will:
// 1. Cancel the upstream request
// 2. Send the interim response
// 3. Store the exchange for later resume
type ForwardWithDetachOption struct {
	DetachSignal <-chan DetachAndWait
}

func (ForwardWithDetachOption) isProxyAction() {}

// ClassifyFunc determines the kind of inbound request and provides a correlation key.
// The correlation key is used to match Type A requests with their corresponding Type B
// resume requests. The caller is responsible for generating secure, unguessable keys.
type ClassifyFunc func(*http.Request) (InboundRequest, error)

// RewriteFunc prepares the upstream request.
// This is analogous to httputil.ReverseProxy's Rewrite function.
type RewriteFunc func(*UpstreamRequest)

// DecideFunc determines how to handle the request after upstream preparation.
// It returns one of: ForwardNow, RespondNow, or DetachAndWait.
type DecideFunc func(*UpstreamRequest) (ProxyAction, error)

// ModifyResponseFunc allows modification of the upstream response before streaming.
// This is analogous to httputil.ReverseProxy's ModifyResponse function.
type ModifyResponseFunc func(*http.Response) error

// ErrorHandlerFunc handles errors during proxy operation.
// This is analogous to httputil.ReverseProxy's ErrorHandler function.
type ErrorHandlerFunc func(http.ResponseWriter, *http.Request, error)

// PendingExchange represents a detached upstream request that can be resumed later.
type PendingExchange interface {
	// Key returns the correlation key for this exchange.
	Key() string

	// WaitResponse waits for the upstream response to be ready and returns it.
	// It blocks until the response is available, the context is canceled,
	// or the exchange times out.
	WaitResponse(ctx context.Context) (*http.Response, error)

	// Cancel cancels the upstream request with the given error.
	Cancel(err error)
}

// PendingStore manages pending exchanges with TTL support.
type PendingStore interface {
	// Put stores a pending exchange with an expiration time.
	Put(key string, exchange PendingExchange, expiresAt time.Time) error

	// Get retrieves a pending exchange by key.
	// Returns false if the key doesn't exist or has expired.
	Get(key string) (PendingExchange, bool)

	// Delete removes a pending exchange.
	Delete(key string) error

	// CleanupExpired removes all expired entries and returns the count removed.
	CleanupExpired() int
}

// BufferPool provides byte buffers for copying response bodies.
type BufferPool interface {
	Get() []byte
	Put([]byte)
}

// Made with Bob
