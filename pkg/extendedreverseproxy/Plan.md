I would like us to design an ExtendedReverseProxy as a go package to be used instead of http.ReverseProxy in k8s sidecar for handling inbound traffic. We will later deal with the details of embedding the ExtendedReverseProxy into sidecar.

The setup is as follows: Client -> sidecar -> Server

The sidecar needs to examine and intervene in server inbound traffic while keeping the server agnostic to the changes made and to whether the flow is Scenario 1 or Scenario 2.

There are two types of incoming HTTP requests:

- Type A: a standard HTTP request sent by a client to the service behind the sidecar.
- Type B: a special HTTP request identified by a special header or query string or URL that serves as a resume signal to the sidecar. The sidecar does not forward this request to the server. It may forward the server response to an earlier Type A request as the response to this Type B request.

There are two scenarios:

- Scenario 1: Type A request is sent by the client, forwarded by the sidecar to the server, and the server response is returned by the sidecar to the same client request.
- Scenario 2: Type A request is sent by the client and forwarded by the sidecar to the server. The server later needs some missing external information. That situation is detected outside the server's normal inbound handler logic, and the sidecar is instructed to return a 401 response, or another intermediate response, to the Type A client. The server does not create that response and remains agnostic to the detour. Later the client sends a Type B resume request, the sidecar consumes it, provides the missing information to the broader flow, and when the server eventually completes its work, the sidecar sends that server response as the response to Type B.

The `http.ReverseProxy` does not offer full control over request and response ownership as needed to implement this design. We can adapt code from `http.ReverseProxy` (See src/net/http/httputil/reverseproxy.go its interface and dependencies.), but the interface definition and detached response model are documented in `Interface.md`.

Here are non-mandatory planning notes that are not repeated in `Interface.md`, or are kept here only as implementation-oriented guidance aligned with that interface.

If you ever update this file, update from here and onwards only!

See `Interface.md` for the interface definition.

### High-level deployment architecture

- **Sidecar**
  - Runs an inbound `net/http` server on the pod-facing port.
  - Uses a shared `http.Client` / `http.Transport` for upstream calls to the app on `http://127.0.0.1:<app-port>`.
- **App**
  - Listens on localhost only.
  - Knows nothing about Type A / Type B, resume tokens, or detached responses.
- **Kubernetes wiring**
  - The Service points to the sidecar.
  - The sidecar forwards to the app on localhost.
  - The app should not be exposed directly.

### Design constraints carried into the interface

- The app must observe a normal HTTP request/response interaction.
- The sidecar may decide whether the upstream response is bound to Type A or to a later Type B request.
- For Scenario 2, the upstream request lifetime should be sidecar-managed and not tied to the completion of Type A.
- Resume tokens / correlation IDs should be unguessable if they provide access to pending responses.
- The token should be propagated consistently:
  - returned to the client in the Type A intermediate response,
  - attached to logs and internal state,
  - and made available for the Type B lookup path.
- If useful for observability, the token may also be injected into the app request headers, but that is an implementation detail and not a required part of the public interface.

### Missing-information detection note

Scenario 2 assumes that some external mechanism determines that the flow must be detached and resumed later. That detection path is intentionally not modeled in detail in the public interface yet.

The current interface models the decision point generically through the action-selection hook. A later design pass may need to define:

- how outbound interception reports missing information,
- how that signal is correlated with the in-flight Type A request,
- whether the detach decision is synchronous or asynchronous relative to starting the upstream round trip.

### Why not a TCP proxy

A raw TCP proxy is not a good fit here because the problem is fundamentally HTTP-aware:

- request classification is based on HTTP headers / query / URL,
- the sidecar must preserve proxy-correct header semantics,
- the sidecar may need to manipulate status code, trailers, and streaming behavior,
- and the sidecar should reuse Go's HTTP server/client machinery instead of reimplementing protocol handling.

### Operational and implementation guidance

#### Timeouts and cleanup

- Detached upstream exchanges must have a TTL.
- If Type B never arrives, the sidecar should cancel the upstream request and remove the pending entry.
- If the app never responds, Type B should eventually receive a timeout-related error.
- Cleanup should occur on success, timeout, cancellation, or upstream failure.

#### Backpressure and streaming

- Scenario 2 should stream the upstream body to Type B as it arrives rather than buffering the whole body.
- If Type B arrives before the upstream response is ready, the sidecar may block waiting, subject to timeout policy.
- Use flushing behavior appropriate for streaming responses such as SSE.

#### Client and backend disconnects

- Respect `r.Context()` for the currently active downstream request:
  - if the Type A client disconnects before the intermediate response is sent, abandon that response,
  - if the Type B client disconnects while waiting or streaming, stop work on that downstream path.
- Distinguish downstream cancellation from upstream cancellation:
  - Type A disconnect should not necessarily cancel the detached upstream request in Scenario 2.
- If the app fails before sending headers, return an error to whichever downstream request currently owns the response path.
- If the app fails mid-body, the downstream client will observe a truncated stream; log and clean up.

#### Header behavior

- Preserve multi-value headers when copying.
- Strip hop-by-hop headers in both directions.
- Parse the `Connection` header and remove any listed hop-by-hop headers as well.
- Decide which internal headers must never be exposed to the external client.

#### Body framing

- Let `net/http` manage chunked encoding and framing.
- Do not set `Transfer-Encoding` manually.
- Forward `Content-Length` only when it is still valid for the body being sent.
- If the body is transformed, remove stale `Content-Length` and allow chunked transfer.

#### Protocol details

- Let Go handle HTTP/1.1 vs HTTP/2 protocol differences.
- Preserve `Expect: 100-continue` behavior by avoiding premature full-body reads when possible.
- Forward trailers correctly when proxying the app response to either Type A or Type B.
- Reuse a shared `http.Transport` with sane idle-connection settings.

#### State management and observability

- Pending detached exchanges need bounded lifetime and explicit cleanup.
- Store access must be concurrency-safe.
- Logs should include the resume token / correlation ID so a full flow can be traced across Type A, Type B, and the app.
- Error semantics should be defined clearly for cases such as:
  - Type B arrives after the app already failed,
  - Type B never arrives but the app completed,
  - resume token is unknown or expired.

### Practical implementation recommendation

Use `httputil.ReverseProxy` as a source of proven implementation details, especially for:

- header copying,
- hop-by-hop stripping,
- trailer handling,
- streaming / flush behavior,
- transport usage,
- and response/error hook patterns.

Do not preserve its hardwired assumption that the upstream response must always be written to the same inbound `ResponseWriter`.




