package filters

import (
	"github.com/golang/protobuf/ptypes"
	"github.com/nlnwa/veidemann-api/go/config/v1"
	logV1 "github.com/nlnwa/veidemann-api/go/log/v1"
	"github.com/nlnwa/veidemann-recorderproxy/constants"
	"github.com/nlnwa/veidemann-recorderproxy/logger"
	"github.com/nlnwa/veidemann-recorderproxy/serviceconnections"
	log "github.com/sirupsen/logrus"
	"net/http"
	"net/url"
	"time"
)

// session variable must be aligned in i386
// see http://golang.org/src/pkg/sync/atomic/doc.go#L41
var sess int64
var closedSess int64

func OpenSessions() int64 {
	return sess - closedSess
}

func (cs *ConnectionState) Init(proxyId int32, conn *serviceconnections.Connections, req *http.Request, uri *url.URL) *ConnectionState {
	cs.conn = conn
	//cs.ctx = req.Context()
	cs.ProxyId = proxyId
	cs.Method = req.Method
	cs.Uri = uri

	cs.JobExecId = req.Header.Get(constants.HeaderJobExecutionId)
	cs.CrawlExecId = req.Header.Get(constants.HeaderCrawlExecutionId)
	cs.RequestId = req.Header.Get(constants.HeaderRequestId)
	if req.Header.Get(constants.HeaderCollectionId) != "" {
		cid := req.Header.Get(constants.HeaderCollectionId)
		cs.CollectionRef = &config.ConfigRef{
			Kind: config.Kind_collection,
			Id:   cid,
		}
	}

	req.Header.Del(constants.HeaderRequestId)
	req.Header.Del(constants.HeaderCrawlExecutionId)
	req.Header.Del(constants.HeaderJobExecutionId)
	req.Header.Del(constants.HeaderCollectionId)

	cs.FetchTimesTamp = time.Now()
	fetchTimeStamp, _ := ptypes.TimestampProto(cs.FetchTimesTamp)

	cs.CrawlLog = &logV1.CrawlLog{
		JobExecutionId: cs.JobExecId,
		ExecutionId:    cs.CrawlExecId,
		FetchTimeStamp: fetchTimeStamp,
		RequestedUri:   uri.String(),
		Method:         cs.Method,
		IpAddress:      cs.Ip,
	}

	//cs.InitDone = true

	cs.log = logger.Log.WithFields(log.Fields{
		"component": "PROXY",
		"method":    req.Method,
		"url":       uri.String(),
		//"session":   rc.Session(),
	})

	cs.log.Infof("New session")
	return cs
}

func (cs *ConnectionState) LogWithContext(componentName string) *logger.Logger {
	var l *logger.Logger
	if cs.log != nil {
		l = cs.log
	} else {
		l = logger.Log
	}
	l = l.WithField("component", componentName)
	return l
}

func (cs *ConnectionState) LogWithContextAndRequest(req *http.Request, componentName string) *logger.Logger {
	var l *logger.Logger

	if cs.log != nil {
		l = cs.log
	} else {
		l = logger.Log.WithFields(log.Fields{
			"method": req.Method,
			"url":    req.URL.String(),
		})
	}
	l = l.WithField("component", componentName)
	return l
}
