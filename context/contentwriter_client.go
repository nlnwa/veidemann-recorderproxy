/*
 * Copyright 2019 National Library of Norway.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *       http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package context

import (
	"context"
	"github.com/nlnwa/veidemann-api/go/contentwriter/v1"
	"github.com/nlnwa/veidemann-recorderproxy/errors"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	otLog "github.com/opentracing/opentracing-go/log"
	"sync"
)

type CwcSession struct {
	contentwriter.ContentWriter_WriteClient
	span      opentracing.Span
	done      bool
	canceled  bool
	m         sync.Mutex
	cwcCtx    context.Context
	ctxCancel context.CancelFunc
}

func (rc *RecordContext) getCwcSession() (*CwcSession, error) {
	if rc.cwc != nil {
		return rc.cwc, nil
	}

	l := LogWithContext(rc.ctx, "PROXY:CWC")

	rc.mutex.Lock()
	defer rc.mutex.Unlock()

	parentSpan := opentracing.SpanFromContext(rc.ctx)
	span := opentracing.StartSpan("ContentWriter session", opentracing.FollowsFrom(parentSpan.Context()))
	cwcCtx, cancel := context.WithCancel(context.Background())
	cwcCtx = opentracing.ContextWithSpan(cwcCtx, span)

	cwc, err := rc.conn.ContentWriterClient().Write(cwcCtx)
	span.LogFields(otLog.String("event", "Started ContentWriter client session"), otLog.Error(err))
	if err != nil {
		l.WithError(err).Warn("Error connecting to ContentWriter")
		span.LogFields(otLog.String("event", "Failed starting ContentWriter session"), otLog.Error(err))
		err = errors.WrapInternalError(err, errors.RuntimeException, "Error connecting to ContentWriter", err.Error())
		cancel()
		return nil, err
	}

	c := &CwcSession{ContentWriter_WriteClient: cwc, span: span, cwcCtx: cwcCtx, ctxCancel: cancel}

	go func() {
		select {
		case <-rc.ctx.Done():
			c.m.Lock()
			defer c.m.Unlock()
			if !c.done {
				c.done = true
				l.Info("ContentWriter client session canceled by client")
				err := rc.cwc.Send(&contentwriter.WriteRequest{
					Value: &contentwriter.WriteRequest_Cancel{Cancel: "Veidemann recorder proxy lost connection to client"},
				})
				if err != nil {
					l.WithError(err).Warn("Error writing to ContentWriter")
				}
				_, err = rc.cwc.CloseAndRecv()
				if err != nil {
					l.WithError(err).Warn("Error closing from ContentWriter")
				}
				span.Finish()
			}
			cancel()
		}
	}()

	rc.cwc = c
	span.LogFields(otLog.String("event", "Started ContentWriter session"))
	return c, nil
}

func (rc *RecordContext) CancelContentWriter(msg string) error {
	if rc.cwc == nil {
		// No ContentWriter session to cancel
		return nil
	}

	l := LogWithContext(rc.ctx, "PROXY:CWC")

	cwc, err := rc.getCwcSession()
	cwc.canceled = true
	if err != nil {
		cwc.span.LogFields(otLog.String("event", "Cancel content writer"), otLog.String("message", msg), otLog.Error(err))
		return err
	}

	cwc.m.Lock()
	defer cwc.m.Unlock()
	if !cwc.done {
		cwc.done = true
		defer cwc.ctxCancel()

		err = cwc.Send(&contentwriter.WriteRequest{Value: &contentwriter.WriteRequest_Cancel{Cancel: msg}})
		if err != nil {
			cwc.span.LogFields(otLog.String("event", "Cancel content writer"), otLog.String("message", msg), otLog.Error(err))
			l.WithError(err).Info("Error sending ContentWriter cancel")
		}
		reply, err := cwc.CloseAndRecv()
		if err != nil {
			cwc.span.LogFields(otLog.String("event", "Cancel content writer"), otLog.String("message", msg), otLog.Error(err))
			l.WithError(err).Info("Error sending ContentWriter cancel")
		} else {
			cwc.span.LogFields(otLog.String("event", "Cancel content writer"), otLog.String("message", msg), otLog.String("reply", reply.String()))
		}
	}
	return err
}

func (rc *RecordContext) SendProtocolHeader(recNum int32, p []byte) error {
	l := LogWithContext(rc.ctx, "PROXY:CWC")

	otEvent := otLog.String("event", "sendProtocolHeader")
	otRecNum := otLog.Int32("recNum", recNum)

	cwc, err := rc.getCwcSession()
	if err != nil {
		cwc.span.LogFields(otEvent, otRecNum, otLog.Error(err))
		return err
	}

	if cwc.canceled {
		return nil
	}

	protocolHeaderRequest := &contentwriter.WriteRequest{
		Value: &contentwriter.WriteRequest_ProtocolHeader{
			ProtocolHeader: &contentwriter.Data{
				RecordNum: recNum,
				Data:      p,
			},
		},
	}

	err = cwc.Send(protocolHeaderRequest)
	if err != nil {
		l.WithError(err).Info("Error sending ContentWriter protocol header")
		cwc.span.LogFields(otEvent, otRecNum, otLog.Error(err))
	} else {
		cwc.span.LogFields(otEvent, otRecNum)
	}
	return err
}

func (rc *RecordContext) SendPayload(recNum int32, p []byte) error {
	l := LogWithContext(rc.ctx, "PROXY:CWC")

	otEvent := otLog.String("event", "sendPayload")
	otRecNum := otLog.Int32("recNum", recNum)

	cwc, err := rc.getCwcSession()
	if err != nil {
		cwc.span.LogFields(otEvent, otRecNum, otLog.Error(err))
		return err
	}

	if cwc.canceled {
		return nil
	}

	payloadRequest := &contentwriter.WriteRequest{
		Value: &contentwriter.WriteRequest_Payload{
			Payload: &contentwriter.Data{
				RecordNum: recNum,
				Data:      p,
			},
		},
	}

	err = cwc.Send(payloadRequest)
	if err != nil {
		l.WithError(err).Info("Error sending ContentWriter payload")
		cwc.span.LogFields(otEvent, otRecNum, otLog.Error(err))
	} else {
		cwc.span.LogFields(otEvent, otRecNum)
	}
	return err
}

func (rc *RecordContext) SendMeta() (reply *contentwriter.WriteReply, err error) {
	l := LogWithContext(rc.ctx, "PROXY:CWC")

	cwc, err := rc.getCwcSession()
	if err != nil {
		cwc.span.LogFields(otLog.String("event", "sendMeta"), otLog.String("http.url", rc.Meta.Meta.TargetUri), otLog.Error(err))
		return nil, err
	}

	if cwc.canceled {
		return nil, nil
	}

	cwc.m.Lock()
	defer cwc.m.Unlock()
	if !cwc.done {
		cwc.done = true
		defer cwc.ctxCancel()

		sendMetaSpan := opentracing.StartSpan("ContentWriter sendMeta", opentracing.ChildOf(cwc.span.Context()))
		defer sendMetaSpan.Finish()
		ext.HTTPUrl.Set(sendMetaSpan, rc.Meta.Meta.TargetUri)
		ext.Component.Set(sendMetaSpan, "contentWriterClient")

		metaRequest := &contentwriter.WriteRequest{
			Value: rc.Meta,
		}

		err = cwc.Send(metaRequest)
		if err != nil {
			l.WithError(err).Info("Error sending ContentWriter meta")
			ext.Error.Set(sendMetaSpan, true)
			sendMetaSpan.LogFields(otLog.String("event", "sendMeta"), otLog.Error(err))
			return nil, err
		}

		reply, err = cwc.CloseAndRecv()
		if err != nil {
			l.WithError(err).Info("Error receiving ContentWriter meta response")
			ext.Error.Set(sendMetaSpan, true)
			cwc.span.LogFields(otLog.String("event", "receiveMeta"), otLog.Error(err))
		}

		cwc.span.Finish()
	}
	return
}
