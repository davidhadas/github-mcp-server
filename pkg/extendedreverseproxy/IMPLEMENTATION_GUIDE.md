# ExtendedReverseProxy Implementation Guide

This document provides detailed implementation guidance for each component of the `extendedreverseproxy` package.

## 1. Types Definition (types.go)

### Core Enums and Structs

```go
package extendedreverseproxy

import (
    "context"
    "log"
    "net/http"
    "time"
)

// RequestKind identifies the type of inbound request
type RequestKind int

const (
    // RequestKindForward indicates a normal request to be proxied (Type A)
    RequestKindForward RequestKind = iota
    
    // RequestKindResume indicates a resume request to retrieve a pending response (Type B)
    RequestKindResume
)

// InboundRequest represents a classified incoming request
type InboundRequest struct {
    Kind RequestKind
    In   *http.Request
    Key  string // Correlation key provided by caller
}

// UpstreamRequest represents a request being prepared for upstream
type UpstreamRequest struct {
    In  *http.Request // Original inbound request
    Out *http.Request // Prepared upstream request
    Key string        // Correlation key for this exchange
}

// ProxyAction represents a decision about how to handle a request
type ProxyAction interface {
    isProxyAction()
}

// ForwardNow streams the upstream response directly to the current request
type ForwardNow struct{}

func (ForwardNow) isProxyAction() {}

// RespondNow sends a local response without waiting for upstream
type RespondNow struct {
    StatusCode int
    Header     http.Header
    Body       []byte
}

func (RespondNow) isProxyAction() {}

// DetachAndWait sends an interim response and keeps the upstream request alive
type DetachAndWait struct {
    InterimStatusCode int
    InterimHeader     http.Header
    InterimBody       []byte
}

func (DetachAndWait) isProxyAction() {}
```

### Hook Function Types

```go
// ClassifyFunc determines the kind of inbound request and provides a correlation key
type ClassifyFunc func(*http.Request) (InboundRequest, error)

// RewriteFunc prepares the upstream request
type RewriteFunc func(*UpstreamRequest)

// DecideFunc determines how to handle the request after upstream preparation
type DecideFunc func(*UpstreamRequest) (ProxyAction, error)

// ModifyResponseFunc allows modification of the upstream response before streaming
type ModifyResponseFunc func(*http.Response) error

// ErrorHandlerFunc handles errors during proxy operation
type ErrorHandlerFunc func(http.ResponseWriter, *http.Request, error)
```

### Interfaces

```go
// PendingExchange represents a detached upstream request
type PendingExchange interface {
    Key() string
    WaitResponse(ctx context.Context) (*http.Response, error)
    Cancel(err error)
}

// PendingStore manages pending exchanges
type PendingStore interface {
    Put(key string, exchange PendingExchange, expiresAt time.Time) error
    Get(key string) (PendingExchange, bool)
    Delete(key string) error
    CleanupExpired() int
}

// BufferPool provides byte buffers for copying
type BufferPool interface {
    Get() []byte
    Put([]byte)
}
```

## 2. Pending Exchange Implementation (pending.go)

### Structure

```go
type pendingExchange struct {
    key         string
    ctx         context.Context
    cancel      context.CancelFunc
    respCh      chan struct{}
    resp        *http.Response
    err         error
    mu          sync.Mutex
}

func newPendingExchange(key string, timeout time.Duration) *pendingExchange {
    ctx, cancel := context.WithTimeout(context.Background(), timeout)
    return &pendingExchange{
        key:    key,
        ctx:    ctx,
        cancel: cancel,
        respCh: make(chan struct{}),
    }
}

func (pe *pendingExchange) Key() string {
    return pe.key
}

func (pe *pendingExchange) WaitResponse(ctx context.Context) (*http.Response, error) {
    select {
    case <-pe.respCh:
        pe.mu.Lock()
        defer pe.mu.Unlock()
        return pe.resp, pe.err
    case <-ctx.Done():
        return nil, ctx.Err()
    case <-pe.ctx.Done():
        return nil, pe.ctx.Err()
    }
}

func (pe *pendingExchange) Cancel(err error) {
    pe.mu.Lock()
    if pe.err == nil {
        pe.err = err
    }
    pe.mu.Unlock()
    pe.cancel()
}

func (pe *pendingExchange) setResponse(resp *http.Response, err error) {
    pe.mu.Lock()
    pe.resp = resp
    pe.err = err
    pe.mu.Unlock()
    close(pe.respCh)
}
```

### Dispatcher Function

```go
func (p *ExtendedReverseProxy) dispatch(req *http.Request, key string) PendingExchange {
    pe := newPendingExchange(key, p.Timeout)
    
    go func() {
        defer pe.cancel()
        
        // Clone request with pending exchange context
        outReq := req.Clone(pe.ctx)
        outReq.RequestURI = "" // Must be empty for client requests
        
        // Perform upstream request
        resp, err := p.transport().RoundTrip(outReq)
        pe.setResponse(resp, err)
    }()
    
    return pe
}
```

## 3. Store Implementation (store.go)

### In-Memory Store

```go
type memoryStore struct {
    mu      sync.RWMutex
    entries map[string]*storeEntry
}

type storeEntry struct {
    exchange  PendingExchange
    expiresAt time.Time
}

func NewMemoryStore() PendingStore {
    return &memoryStore{
        entries: make(map[string]*storeEntry),
    }
}

func (s *memoryStore) Put(key string, exchange PendingExchange, expiresAt time.Time) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if _, exists := s.entries[key]; exists {
        return fmt.Errorf("key already exists: %s", key)
    }
    
    s.entries[key] = &storeEntry{
        exchange:  exchange,
        expiresAt: expiresAt,
    }
    return nil
}

func (s *memoryStore) Get(key string) (PendingExchange, bool) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    
    entry, ok := s.entries[key]
    if !ok {
        return nil, false
    }
    
    // Check expiration
    if time.Now().After(entry.expiresAt) {
        return nil, false
    }
    
    return entry.exchange, true
}

func (s *memoryStore) Delete(key string) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    delete(s.entries, key)
    return nil
}

func (s *memoryStore) CleanupExpired() int {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    now := time.Now()
    count := 0
    
    for key, entry := range s.entries {
        if now.After(entry.expiresAt) {
            entry.exchange.Cancel(fmt.Errorf("expired"))
            delete(s.entries, key)
            count++
        }
    }
    
    return count
}
```

## 4. Header Utilities (headers.go)

### Hop-by-Hop Headers

```go
// Hop-by-hop headers that should not be forwarded
var hopHeaders = []string{
    "Connection",
    "Proxy-Connection",
    "Keep-Alive",
    "Proxy-Authenticate",
    "Proxy-Authorization",
    "Te",
    "Trailer",
    "Transfer-Encoding",
    "Upgrade",
}

func removeHopByHopHeaders(h http.Header) {
    // Remove standard hop-by-hop headers
    for _, header := range hopHeaders {
        h.Del(header)
    }
}

func removeConnectionHeaders(h http.Header) {
    // Parse Connection header and remove listed headers
    for _, f := range h["Connection"] {
        for _, sf := range strings.Split(f, ",") {
            if sf = strings.TrimSpace(sf); sf != "" {
                h.Del(sf)
            }
        }
    }
}
```

### Header Copying

```go
func copyHeaders(dst, src http.Header) {
    for k, vv := range src {
        for _, v := range vv {
            dst.Add(k, v)
        }
    }
}

func announceTrailers(w http.ResponseWriter, resp *http.Response) {
    if len(resp.Trailer) == 0 {
        return
    }
    
    var trailerKeys []string
    for k := range resp.Trailer {
        trailerKeys = append(trailerKeys, k)
    }
    w.Header().Add("Trailer", strings.Join(trailerKeys, ", "))
}
```

## 5. Streaming Utilities (streaming.go)

### Response Streaming

```go
func streamResponse(w http.ResponseWriter, resp *http.Response, flushInterval time.Duration, bufferPool BufferPool) error {
    defer resp.Body.Close()
    
    // Copy response headers
    copyHeaders(w.Header(), resp.Header)
    removeConnectionHeaders(w.Header())
    removeHopByHopHeaders(w.Header())
    
    // Announce trailers
    announceTrailers(w, resp)
    
    // Write status code
    w.WriteHeader(resp.StatusCode)
    
    // Stream body with flushing
    if err := copyWithFlush(w, resp.Body, flushInterval, bufferPool); err != nil {
        return err
    }
    
    // Copy trailers
    copyHeaders(w.Header(), resp.Trailer)
    
    return nil
}

func copyWithFlush(dst io.Writer, src io.Reader, flushInterval time.Duration, bufferPool BufferPool) error {
    if flushInterval <= 0 {
        _, err := io.Copy(dst, src)
        return err
    }
    
    flusher, ok := dst.(http.Flusher)
    if !ok {
        _, err := io.Copy(dst, src)
        return err
    }
    
    buf := bufferPool.Get()
    defer bufferPool.Put(buf)
    
    lastFlush := time.Now()
    
    for {
        n, err := src.Read(buf)
        if n > 0 {
            if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
                return writeErr
            }
            
            if time.Since(lastFlush) > flushInterval {
                flusher.Flush()
                lastFlush = time.Now()
            }
        }
        
        if err == io.EOF {
            flusher.Flush()
            return nil
        }
        if err != nil {
            return err
        }
    }
}
```

## 6. Main Proxy Implementation (proxy.go)

### Proxy Structure

```go
type ExtendedReverseProxy struct {
    // Required hooks
    Classify ClassifyFunc
    Rewrite  RewriteFunc
    Decide   DecideFunc
    
    // Optional hooks
    ModifyResponse ModifyResponseFunc
    ErrorHandler   ErrorHandlerFunc
    
    // Transport configuration
    Transport     http.RoundTripper
    FlushInterval time.Duration
    BufferPool    BufferPool
    
    // Logging
    ErrorLog *log.Logger
    
    // Detached exchange management
    Store   PendingStore
    Timeout time.Duration
    
    // Internal state
    cleanupOnce sync.Once
    cleanupDone chan struct{}
}
```

### ServeHTTP Implementation

```go
func (p *ExtendedReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    p.startCleanup()
    
    // Classify request
    inbound, err := p.Classify(r)
    if err != nil {
        p.handleError(w, r, fmt.Errorf("classification failed: %w", err))
        return
    }
    
    switch inbound.Kind {
    case RequestKindForward:
        p.handleForward(w, inbound)
    case RequestKindResume:
        p.handleResume(w, inbound)
    default:
        p.handleError(w, r, fmt.Errorf("unknown request kind: %d", inbound.Kind))
    }
}
```

### Forward Handler (Type A)

```go
func (p *ExtendedReverseProxy) handleForward(w http.ResponseWriter, inbound InboundRequest) {
    // Prepare upstream request
    outReq := inbound.In.Clone(inbound.In.Context())
    outReq.RequestURI = ""
    
    // Remove hop-by-hop headers
    removeConnectionHeaders(outReq.Header)
    removeHopByHopHeaders(outReq.Header)
    
    upstream := &UpstreamRequest{
        In:  inbound.In,
        Out: outReq,
        Key: inbound.Key,
    }
    
    // Rewrite
    if p.Rewrite != nil {
        p.Rewrite(upstream)
    }
    
    // Decide action
    action, err := p.Decide(upstream)
    if err != nil {
        p.handleError(w, inbound.In, err)
        return
    }
    
    switch act := action.(type) {
    case ForwardNow:
        p.doForwardNow(w, inbound.In, upstream)
    case RespondNow:
        p.doRespondNow(w, act)
    case DetachAndWait:
        p.doDetachAndWait(w, inbound.In, upstream, act)
    default:
        p.handleError(w, inbound.In, fmt.Errorf("unknown action type"))
    }
}
```

### Action Handlers

```go
func (p *ExtendedReverseProxy) doForwardNow(w http.ResponseWriter, r *http.Request, upstream *UpstreamRequest) {
    resp, err := p.transport().RoundTrip(upstream.Out)
    if err != nil {
        p.handleError(w, r, err)
        return
    }
    
    if p.ModifyResponse != nil {
        if err := p.ModifyResponse(resp); err != nil {
            resp.Body.Close()
            p.handleError(w, r, err)
            return
        }
    }
    
    if err := streamResponse(w, resp, p.FlushInterval, p.bufferPool()); err != nil {
        p.logf("error streaming response: %v", err)
    }
}

func (p *ExtendedReverseProxy) doRespondNow(w http.ResponseWriter, action RespondNow) {
    copyHeaders(w.Header(), action.Header)
    w.WriteHeader(action.StatusCode)
    if len(action.Body) > 0 {
        w.Write(action.Body)
    }
}

func (p *ExtendedReverseProxy) doDetachAndWait(w http.ResponseWriter, r *http.Request, upstream *UpstreamRequest, action DetachAndWait) {
    // Create pending exchange
    pe := p.dispatch(upstream.Out, upstream.Key)
    
    // Store it
    expiresAt := time.Now().Add(p.Timeout)
    if err := p.Store.Put(upstream.Key, pe, expiresAt); err != nil {
        pe.Cancel(err)
        p.handleError(w, r, fmt.Errorf("failed to store pending exchange: %w", err))
        return
    }
    
    // Send interim response
    copyHeaders(w.Header(), action.InterimHeader)
    w.WriteHeader(action.InterimStatusCode)
    if len(action.InterimBody) > 0 {
        w.Write(action.InterimBody)
    }
}
```

### Resume Handler (Type B)

```go
func (p *ExtendedReverseProxy) handleResume(w http.ResponseWriter, inbound InboundRequest) {
    // Lookup pending exchange
    pe, ok := p.Store.Get(inbound.Key)
    if !ok {
        http.Error(w, "pending exchange not found", http.StatusNotFound)
        return
    }
    
    // Clean up after we're done
    defer p.Store.Delete(inbound.Key)
    
    // Wait for response
    resp, err := pe.WaitResponse(inbound.In.Context())
    if err != nil {
        p.handleError(w, inbound.In, fmt.Errorf("upstream failed: %w", err))
        return
    }
    
    if p.ModifyResponse != nil {
        if err := p.ModifyResponse(resp); err != nil {
            resp.Body.Close()
            p.handleError(w, inbound.In, err)
            return
        }
    }
    
    // Stream response
    if err := streamResponse(w, resp, p.FlushInterval, p.bufferPool()); err != nil {
        p.logf("error streaming response: %v", err)
    }
}
```

### Helper Methods

```go
func (p *ExtendedReverseProxy) transport() http.RoundTripper {
    if p.Transport != nil {
        return p.Transport
    }
    return http.DefaultTransport
}

func (p *ExtendedReverseProxy) bufferPool() BufferPool {
    if p.BufferPool != nil {
        return p.BufferPool
    }
    return defaultBufferPool
}

func (p *ExtendedReverseProxy) handleError(w http.ResponseWriter, r *http.Request, err error) {
    if p.ErrorHandler != nil {
        p.ErrorHandler(w, r, err)
        return
    }
    
    p.logf("proxy error: %v", err)
    http.Error(w, "Bad Gateway", http.StatusBadGateway)
}

func (p *ExtendedReverseProxy) logf(format string, args ...interface{}) {
    if p.ErrorLog != nil {
        p.ErrorLog.Printf(format, args...)
    } else {
        log.Printf(format, args...)
    }
}
```

### Cleanup

```go
func (p *ExtendedReverseProxy) startCleanup() {
    p.cleanupOnce.Do(func() {
        p.cleanupDone = make(chan struct{})
        go p.cleanupLoop()
    })
}

func (p *ExtendedReverseProxy) cleanupLoop() {
    ticker := time.NewTicker(time.Minute)
    defer ticker.Stop()
    
    for {
        select {
        case <-ticker.C:
            count := p.Store.CleanupExpired()
            if count > 0 {
                p.logf("cleaned up %d expired pending exchanges", count)
            }
        case <-p.cleanupDone:
            return
        }
    }
}

func (p *ExtendedReverseProxy) Close() error {
    if p.cleanupDone != nil {
        close(p.cleanupDone)
    }
    return nil
}
```

## 7. Default Buffer Pool

```go
var defaultBufferPool = &syncBufferPool{}

type syncBufferPool struct {
    pool sync.Pool
}

func (p *syncBufferPool) Get() []byte {
    if v := p.pool.Get(); v != nil {
        return v.([]byte)
    }
    return make([]byte, 32*1024)
}

func (p *syncBufferPool) Put(b []byte) {
    p.pool.Put(b)
}
```

## Implementation Checklist

- [ ] Create types.go with all type definitions
- [ ] Implement pending.go with pendingExchange
- [ ] Implement store.go with memoryStore
- [ ] Implement headers.go with header utilities
- [ ] Implement streaming.go with streaming utilities
- [ ] Implement proxy.go with main proxy logic
- [ ] Add comprehensive error handling
- [ ] Add logging throughout
- [ ] Write unit tests for each component
- [ ] Write integration tests for both scenarios
- [ ] Add package documentation
- [ ] Create usage examples

## Testing Approach

Use `httptest.Server` for integration tests to simulate real HTTP interactions without mocking.

Example test structure:
1. Create upstream test server
2. Configure proxy with test hooks
3. Send Type A request
4. Verify interim response (Scenario 2)
5. Send Type B request
6. Verify final response matches upstream

This provides realistic testing of the full request/response lifecycle.