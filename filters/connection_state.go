package filters

import (
	"context"
	"github.com/nlnwa/veidemann-api/go/config/v1"
	"github.com/nlnwa/veidemann-api/go/contentwriter/v1"
	logV1 "github.com/nlnwa/veidemann-api/go/log/v1"
	"github.com/nlnwa/veidemann-recorderproxy/logger"
	"github.com/nlnwa/veidemann-recorderproxy/serviceconnections"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// ConnectionState holds data about a connection. No data is ever guaranteed to be present - the
// getter methods, such as ConnectionState.Upstream, may return zero values for missing data.
type ConnectionState struct {
	// Values from this connection's first request.
	originalURLScheme string
	originalURLHost   string
	originalHost      string

	upstream     net.Conn
	upstreamAddr string

	// It is sometimes necessary to delay retrieval of the downstream connection. The function
	// itself is never nil, but the returned net.Conn may be.
	downstream func() net.Conn

	requestNumber int

	// Is this part of a MITM'd connection?
	mitming bool

	// Used by proxy.RequestAware connections.
	requestAwareRequest  *http.Request
	requestAwareUpstream net.Conn

	session           int64
	Error             error
	ConnectErr        error
	conn              *serviceconnections.Connections
	ctx               context.Context
	cwc               *CwcSession
	bcc               *BccSession
	Method            string
	Uri               *url.URL
	Host              string
	Port              string
	Ip                string
	JobExecId         string
	CrawlExecId       string
	RequestId         string
	CollectionRef     *config.ConfigRef
	FetchTimesTamp    time.Time
	Meta              *contentwriter.WriteRequest_Meta
	CrawlLog          *logV1.CrawlLog
	ReplacementScript *config.BrowserScript
	closed            bool
	FoundInCache      bool
	PrecludedByRobots bool
	done              bool
	mutex             sync.Mutex
	//InitDone          bool
	ProxyId int32
	log     *logger.Logger
}

// NewConnectionState creates a new ConnectionState object. Any of the inputs may be nil.
func NewConnectionState(Conn *serviceconnections.Connections, initialReq *http.Request, upstream, downstream net.Conn) *ConnectionState {
	cs := &ConnectionState{
		upstream:      upstream,
		downstream:    func() net.Conn { return downstream },
		requestNumber: 1,
		mitming:       false,
		conn:          Conn,
		session:       atomic.AddInt64(&sess, 1),
	}
	if initialReq != nil {
		cs.originalURLScheme = initialReq.URL.Scheme
		cs.originalURLHost = initialReq.URL.Host
		cs.originalHost = initialReq.Host
		cs.Method = initialReq.Method
		cs.ctx = initialReq.Context()
	}
	if upstream != nil {
		cs.upstreamAddr = upstream.RemoteAddr().String()
	}
	return cs
}

// Downstream returns the downstream connection.
func (cs *ConnectionState) Downstream() net.Conn {
	return cs.downstream()
}

// Upstream returns the upstream connection.
func (cs *ConnectionState) Upstream() net.Conn {
	return cs.upstream
}

// UpstreamAddr returns the address of the upstream connection.
func (cs *ConnectionState) UpstreamAddr() string {
	return cs.upstreamAddr
}

// RequestNumber returns the current request number for this connection.
func (cs *ConnectionState) RequestNumber() int {
	return cs.requestNumber
}

// OriginalURLScheme is the value of URL.Scheme taken from the first request received on this
// connection.
func (cs *ConnectionState) OriginalURLScheme() string {
	return cs.originalURLScheme
}

// OriginalURLHost is the value of URL.Host taken from the first request received on this
// connection.
func (cs *ConnectionState) OriginalURLHost() string {
	return cs.originalURLHost
}

// OriginalURLScheme is the value of Host taken from the first request received on this connection.
func (cs *ConnectionState) OriginalHost() string {
	return cs.originalHost
}

// IsMITMing returns true if this connection is part of a MITM'd connection.
func (cs *ConnectionState) IsMITMing() bool {
	return cs.mitming
}

// RequestAwareRequest is the request used by RequestAware connections.
func (cs *ConnectionState) RequestAwareRequest() *http.Request {
	return cs.requestAwareRequest
}

// RequestAwareUpstream is the upstream connection used by RequestAware connections.
func (cs *ConnectionState) RequestAwareUpstream() net.Conn {
	return cs.requestAwareUpstream
}

// Clone this object.
func (cs *ConnectionState) Clone() *ConnectionState {
	return &ConnectionState{
		cs.originalURLScheme, cs.originalURLHost, cs.originalHost,
		cs.upstream, cs.upstreamAddr, cs.downstream,
		cs.requestNumber,
		cs.mitming,
		cs.requestAwareRequest,
		cs.requestAwareUpstream,
		cs.session,
		cs.Error,
		cs.ConnectErr,
		cs.conn,
		cs.ctx,
		cs.cwc,
		cs.bcc,
		cs.Method,

		cs.Uri,
		cs.Host,
		cs.Port,
		cs.Ip,
		cs.JobExecId,
		cs.CrawlExecId, //cs.Init(f.proxyId, f.conn, req, uri)

		cs.RequestId,
		cs.CollectionRef,
		cs.FetchTimesTamp,
		cs.Meta,
		cs.CrawlLog,
		cs.ReplacementScript,
		cs.closed,
		cs.FoundInCache,
		cs.PrecludedByRobots,
		cs.done,
		cs.mutex,
		cs.ProxyId,
		cs.log,
	}
}

// IncrementRequestNumber increments the counter tracking the number of requests on this connection.
func (cs *ConnectionState) IncrementRequestNumber() {
	cs.requestNumber++
}

// SetUpstream sets the upstream connection.
func (cs *ConnectionState) SetUpstream(upstream net.Conn) {
	cs.upstream = upstream
	cs.upstreamAddr = upstream.RemoteAddr().String()
}

// SetUpstreamAddr sets the upstream address.
func (cs *ConnectionState) SetUpstreamAddr(upstreamAddr string) {
	cs.upstreamAddr = upstreamAddr
}

// ClearUpstream clears data about the upstream connection such that Upstream and UpstreamAddr
// return zero values.
func (cs *ConnectionState) ClearUpstream() {
	cs.upstream = nil
	cs.upstreamAddr = ""
}

// SetMITMing is used to mark or unmark this connection as part of a MITM'd connection.
func (cs *ConnectionState) SetMITMing(isMITMing bool) {
	cs.mitming = isMITMing
}

// SetRequestAwareRequest sets the request used by RequestAware connections.
func (cs *ConnectionState) SetRequestAwareRequest(req *http.Request) {
	cs.requestAwareRequest = req
}

// SetRequestAwareUpstream sets the upstream connection used by RequestAware connections.
func (cs *ConnectionState) SetRequestAwareUpstream(upstream net.Conn) {
	cs.requestAwareUpstream = upstream
}
