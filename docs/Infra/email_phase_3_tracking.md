# Phase 3: Real-Time Tracking Proxy

## Objective
Build a lightweight, stateless HTTP proxy server in Go to track email opens and clicks. By running this ourselves, we bypass expensive third-party middleware APIs, retain 100% data ownership, and avoid standard tracking domains that get caught by corporate spam filters.

## Core Requirements

1. **HTTP Proxy Router (`go/cmd/tracking_proxy/main.go` or integrated into `api`)**
   - Must be incredibly fast and stateless.
   - **Click Endpoint (`GET /c/:hash`)**:
     - Decrypt the short hash to retrieve the target URL and prospect ID.
     - Publish `analytics.metrics.logged` with `{event_type: "CLICK"}` to NATS asynchronously to prevent blocking the HTTP response.
     - Respond instantly with `HTTP 302 Found` and the `Location: <target_url>` header.
   - **Open Endpoint (`GET /o/:hash`)**:
     - Decrypt the short hash.
     - Publish `analytics.metrics.logged` with `{event_type: "OPEN"}` to NATS asynchronously.
     - Respond with a 1x1 transparent tracking GIF and correct MIME type (`image/gif`).

2. **Link Rewriter (Used by `email_dispatch_worker.go`)**
   - Create a utility function to encrypt original URLs into the short hashes using AES or Base62 encoding combined with the prospect ID.

## Success Criteria
- Navigating to `/c/abc123hash` redirects seamlessly to `https://usetoro.io`.
- An `analytics.metrics.logged` message appears on the NATS stream with sub-10ms latency.
