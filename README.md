[![License Apache](https://img.shields.io/github/license/nlnwa/veidemann-recorderproxy.svg)](https://github.com/nlnwa/veidemann-recorderproxy/blob/master/LICENSE)
[![GitHub release](https://img.shields.io/github/release/nlnwa/veidemann-recorderproxy.svg)](https://github.com/nlnwa/veidemann-recorderproxy/releases/latest)
[![Build Status](https://travis-ci.org/nlnwa/veidemann-recorderproxy.svg?branch=master)](https://travis-ci.org/nlnwa/veidemann-recorderproxy)
[![Go Report Card](https://goreportcard.com/badge/github.com/nlnwa/veidemann-recorderproxy)](https://goreportcard.com/report/github.com/nlnwa/veidemann-recorderproxy)

# veidemann-recorderproxy

```mermaid
sequenceDiagram
    Browser->>+Proxy: HTTP request
    Proxy->>+BrowserControllwer: New session
    BrowserControllwer->>Proxy: ID
    Proxy->>+ContentWriter: HTTP request headers
    Proxy->>ContentWriter: HTTP request payload
    Proxy->>+Upstream: HTTP request
    Upstream->>-Proxy: HTTP response
    Proxy->>ContentWriter: HTTP response headers
    Proxy->>ContentWriter: HTTP response payload
    Proxy->>ContentWriter: Metadata
    ContentWriter->>-Proxy: Metadata
    Proxy->>BrowserControllwer: CrawlLog
    BrowserControllwer->>-Proxy: x
    Proxy->>-Browser: HTTP response
```
