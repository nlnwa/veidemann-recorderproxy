package proxy

import (
	"context"
	"fmt"
	"github.com/nlnwa/veidemann-recorderproxy/serviceconnections"
	"io"
	"net"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/getlantern/errors"
	"github.com/getlantern/golog"
	"github.com/getlantern/mitm"
	"github.com/nlnwa/veidemann-recorderproxy/filters"
)

const (
	defaultDialTimeout = 30 * time.Second

	serverTimingHeader = "Server-Timing"

	// MetricDialUpstream is the Server-Timing metric to record milliseconds to
	// dial upstream site when handling a CONNECT request.
	MetricDialUpstream = "dialupstream"

	// DialTimeoutHeader is a header that sets a timeout for dialing upstream
	// (only respected on CONNECT requests). The timeout is specified in
	// milliseconds.
	DialTimeoutHeader = "X-Lantern-Dial-Timeout"
)

var (
	log = golog.LoggerFor("proxy")
)

// DialFunc is the dial function to use for dialing the proxy.
type DialFunc func(ctx context.Context, cs *filters.ConnectionState, isCONNECT bool, network, addr string) (conn net.Conn, err error)

// Proxy is a proxy that can handle HTTP(S) traffic
type Proxy interface {
	// Handle handles a single connection, with in specified separately in case
	// there's a buffered reader or something of that sort in use.
	// dialCtx applies only when dialing upstream.
	Handle(dialCtx context.Context, in io.Reader, conn net.Conn) error

	// Connect opens a CONNECT tunnel to the origin without requiring a CONNECT
	// request to first be sent on conn. It will not reply with CONNECT OK.
	// dialCtx applies only when dialing upstream.
	Connect(dialCtx context.Context, in io.Reader, conn net.Conn, origin string) error

	// Serve runs a server on the given Listener
	Serve(l net.Listener) error

	// ApplyMITMOptions updates the configuration of the MITM interceptor
	ApplyMITMOptions(MITMOpts *mitm.Opts) error
}

// WriteResponseInvoker is called by WriteResponseInterceptorFunc to complete writing to downstream.
type WriteResponseInvoker func(cs *filters.ConnectionState, downstream io.Writer, req *http.Request, resp *http.Response) error

// WriteResponseInterceptorFunc intercepts the execution of writing response to downstream.
// invoker is the handler to complete writing to downstream and it is the responsibility of the interceptor to call it.
type WriteResponseInterceptorFunc func(cs *filters.ConnectionState, downstream io.Writer, req *http.Request, resp *http.Response, invoker WriteResponseInvoker) error

// RequestAware is an interface for connections that are able to modify requests
// before they're sent on the connection.
type RequestAware interface {
	// OnRequest allows this connection to make modifications to the request as
	// needed
	OnRequest(req *http.Request)
}

// ResponseAware is an interface for connections that are interested in knowing
// about responses received on the connection.
type ResponseAware interface {
	// OnResponse allows this connection to learn about responses
	OnResponse(req *http.Request, resp *http.Response, err error)
}

// Opts defines options for configuring a Proxy
type Opts struct {
	// IdleTimeout, if specified, lets us know to include an appropriate
	// KeepAlive: timeout header in the responses.
	IdleTimeout time.Duration

	// BufferSource specifies a BufferSource, leave nil to use default.
	BufferSource BufferSource

	// Filter is an optional Filter that will be invoked for every Request
	Filter filters.Filter

	// OnError, if specified, can return a response to be presented to the client
	// in the event that there's an error round-tripping upstream. If the function
	// returns no response, nothing is written to the client. Read indicates
	// whether the error occurred on reading a request or not. (HTTP only)
	OnError func(cs *filters.ConnectionState, req *http.Request, read bool, err error) *http.Response

	// OKWaitsForUpstream specifies whether or not to wait on dialing upstream
	// before responding OK to a CONNECT request (CONNECT only).
	OKWaitsForUpstream bool
	// OKSendsServerTiming specifies whether or not to send back Server-Timing header
	// when responding OK to CONNECT request.
	// See https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Server-Timing.
	// The only metric for now is dialupstream, so the value is in the form "dialupstream;dur=42".
	OKSendsServerTiming bool

	// Dial is the function that's used to dial upstream.
	Dial DialFunc

	// ShouldMITM is an optional function for determining whether or not the given
	// HTTP CONNECT request to the given upstreamAddr is eligible for being MITM'ed.
	ShouldMITM func(req *http.Request, upstreamAddr string) bool

	// MITMOpts, if specified, instructs proxy to attempt to man-in-the-middle
	// connections and handle them as an HTTP proxy. If the connection cannot be
	// mitm'ed (e.g. Client Hello doesn't include an SNI header) or if the
	// contents isn't HTTP, the connection is handled as normal without MITM.
	MITMOpts *mitm.Opts

	// InitMITM is an optional function to initialize the MITM interceptor
	InitMITM func() (MITMInterceptor, error)

	// Optional function to intercept downstream writing of response
	WriteResponseInterceptor WriteResponseInterceptorFunc

	Conn *serviceconnections.Connections
}

type MITMInterceptor interface {
	MITM(cs *filters.ConnectionState, downstream net.Conn, upstream net.Conn) (newDown net.Conn, newUp net.Conn, success bool, err error)
}

type proxy struct {
	*Opts
	//mitmIC *mitm.Interceptor
	mitmIC      MITMInterceptor
	mitmDomains []*regexp.Regexp
	mitmLock    sync.RWMutex
}

// New creates a new Proxy configured with the specified Opts. If there's an
// error initializing MITM, this returns an mitmErr, however the proxy is still
// usable (it just won't MITM).
func New(opts *Opts) (newProxy Proxy, mitmErr error) {
	if opts.Dial == nil {
		opts.Dial = func(ctx context.Context, cs *filters.ConnectionState, isCONNECT bool, network, addr string) (conn net.Conn, err error) {
			ctx, cancel := context.WithTimeout(ctx, defaultDialTimeout)
			defer cancel()
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}
	}
	p := &proxy{
		Opts:        opts,
		mitmDomains: make([]*regexp.Regexp, 0),
	}
	p.applyHTTPDefaults()
	p.applyCONNECTDefaults()

	return p, p.ApplyMITMOptions(opts.MITMOpts)
}

func (proxy *proxy) ApplyMITMOptions(MITMOpts *mitm.Opts) (mitmErr error) {
	proxy.mitmLock.Lock()
	defer proxy.mitmLock.Unlock()

	if MITMOpts != nil {
		//p.mitmIC, mitmErr = mitm.Configure(MITMOpts)
		mitm, mitmErr := mitm.Configure(MITMOpts)
		proxy.mitmIC = &errorForwardingMITMInterceptor{mitm}
		if mitmErr != nil {
			mitmErr = errors.New("Unable to configure MITM: %v", mitmErr)
		} else {
			for _, domain := range MITMOpts.Domains {
				re, err := domainToRegex(domain)
				if err != nil {
					log.Errorf("Unable to convert domain %v to regex: %v", domain, err)
				} else {
					proxy.mitmDomains = append(proxy.mitmDomains, re)
				}
			}
		}
	}
	return
}

// OnFirstOnly returns a filter that applies the given filter only on the first
// request on a given connection.
func OnFirstOnly(filter filters.Filter) filters.Filter {
	return filters.FilterFunc(func(cs *filters.ConnectionState, req *http.Request, next filters.Next) (*http.Response, *filters.ConnectionState, error) {
		requestNumber := cs.RequestNumber()
		if requestNumber == 1 {
			return filter.Apply(cs, req, next)
		}
		return next(cs, req)
	})
}

func (opts *Opts) addIdleKeepAlive(header http.Header) {
	if opts.IdleTimeout > 0 {
		// Tell the client when we're going to time out due to idle connections
		header.Set("Keep-Alive", fmt.Sprintf("timeout=%d", int(opts.IdleTimeout.Seconds())-2))
	}
}

type errorForwardingMITMInterceptor struct {
	*mitm.Interceptor
}

func (e *errorForwardingMITMInterceptor) MITM(cs *filters.ConnectionState, downstream net.Conn, upstream net.Conn) (newDown net.Conn, newUp net.Conn, success bool, err error) {
	newDown, newUp, success, err = e.Interceptor.MITM(downstream, upstream)
	if err != nil && cs.ConnectErr == nil {
		cs.ConnectErr = err
	}
	err = nil
	return
}
