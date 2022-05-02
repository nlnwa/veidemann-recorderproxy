[![License Apache](https://img.shields.io/github/license/nlnwa/veidemann-recorderproxy.svg)](https://github.com/nlnwa/veidemann-recorderproxy/blob/master/LICENSE)
[![GitHub release](https://img.shields.io/github/release/nlnwa/veidemann-recorderproxy.svg)](https://github.com/nlnwa/veidemann-recorderproxy/releases/latest)
[![Build Status](https://travis-ci.org/nlnwa/veidemann-recorderproxy.svg?branch=master)](https://travis-ci.org/nlnwa/veidemann-recorderproxy)
[![Go Report Card](https://goreportcard.com/badge/github.com/nlnwa/veidemann-recorderproxy)](https://goreportcard.com/report/github.com/nlnwa/veidemann-recorderproxy)

# veidemann-recorderproxy

```mermaid
sequenceDiagram
    autonumber
    participant Browser
    participant BrowserController
    participant Proxy
    participant ContentWriter
    participant Upstream
    Browser->>+Proxy: HTTP request
    Proxy->>+BrowserController: New session
    BrowserController->>Proxy: ID
    Proxy->>+ContentWriter: HTTP request headers
    Proxy->>ContentWriter: HTTP request payload
    Proxy->>+Upstream: HTTP request
    Upstream->>Proxy: HTTP response headers
    par
        Proxy->>ContentWriter: HTTP response headers
    and
        Proxy->>Browser: HTTP response headers
    end
    loop until all data received
        Upstream->>-Proxy: HTTP response payload
        par
            Proxy->>Browser: HTTP response payload
        and
            Proxy->>ContentWriter: HTTP response payload
        end
    end
    Proxy->>ContentWriter: Metadata
    ContentWriter->>-Proxy: Metadata
    Proxy->>BrowserController: CrawlLog
    BrowserController->>-Proxy: x
    Proxy->>-Browser: HTTP response
```
