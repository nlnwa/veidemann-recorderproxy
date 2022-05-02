[![License Apache](https://img.shields.io/github/license/nlnwa/veidemann-recorderproxy.svg)](https://github.com/nlnwa/veidemann-recorderproxy/blob/master/LICENSE)
[![GitHub release](https://img.shields.io/github/release/nlnwa/veidemann-recorderproxy.svg)](https://github.com/nlnwa/veidemann-recorderproxy/releases/latest)
[![Build Status](https://travis-ci.org/nlnwa/veidemann-recorderproxy.svg?branch=master)](https://travis-ci.org/nlnwa/veidemann-recorderproxy)
[![Go Report Card](https://goreportcard.com/badge/github.com/nlnwa/veidemann-recorderproxy)](https://goreportcard.com/report/github.com/nlnwa/veidemann-recorderproxy)

# veidemann-recorderproxy

### Proxy's communication with other components for one URL
```mermaid
sequenceDiagram
    autonumber
    participant Browser
    participant BrowserController
    participant Proxy
    participant ContentWriter
    participant Upstream
    Browser->>+Proxy: HTTP request
    Proxy->>BrowserController: Register New
    activate BrowserController
    BrowserController->>Proxy: Config
    rect rgb(250,250,255)
        Note over Proxy: Check the config to see if robots.txt allow us to crawl the URL
        opt Allowed to crawl
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
                Proxy->>BrowserController: Notify Activity [Data received]
            end
            Proxy->>BrowserController: Notify Activity [All data received]
            Proxy->>ContentWriter: Metadata
            ContentWriter->>-Proxy: Metadata
        end
    end
    Proxy->>BrowserController: Completed (CrawlLog)
    deactivate BrowserController
    Proxy->>-Browser: HTTP response
```
