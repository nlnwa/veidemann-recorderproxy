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

package main

import (
	"crypto/tls"
	"fmt"
	"github.com/nlnwa/veidemann-recorderproxy/errors"
	"github.com/nlnwa/veidemann-recorderproxy/logging"
	"github.com/nlnwa/veidemann-recorderproxy/recorderproxy"
	log "github.com/sirupsen/logrus"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
	"net/http"
	"net/http/httptest"
	"net/url"
	"time"
)

var (
	acceptAllCerts = &tls.Config{InsecureSkipVerify: true}
)

func main() {
	flag.BoolP("verbose", "v", false, "should every proxy request be logged to stdout")
	flag.BoolP("help", "h", false, "Usage instructions")
	flag.String("log-level", "info", "log level, available levels are panic, fatal, error, warn, info, debug and trace")
	flag.String("log-formatter", "text", "log formatter, available values are text, logfmt and json")
	flag.Bool("log-method", false, "log method name")
	flag.Parse()
	viper.BindPFlags(flag.CommandLine)

	logging.InitLog(viper.GetString("log-level"), viper.GetString("log-formatter"), viper.GetBool("log-method"))

	if flag.NArg() != 1 || viper.GetBool("help") {
		flag.Usage()
		return
	}

	url := flag.Arg(0)

	grpcServices := NewGrpcServiceMock()
	client := newProxy(grpcServices)

	clientTimeout := 1500 * time.Millisecond

	statusCode, got, err := get(url, client, clientTimeout)
	if grpcServices.doneBC != nil {
		<-grpcServices.doneBC
	}
	if grpcServices.doneCW != nil {
		<-grpcServices.doneCW
	}

	log.Infof("Status: %v", statusCode)

	if len(got) > 0 {
		fmt.Printf("\n%s... (%d bytes)\n\n", got[0:10], len(got))
	}

	if err != nil {
		errors.LogError(errors.InvalidRequest, err.Error())
	}
}

func newProxy(mock *GrpcServiceMock) *http.Client {
	conn := recorderproxy.NewConnections()
	conn.StatsHandlerFactory = NewStatsHandler
	err := conn.Connect("", "", "", "", "", "", 1*time.Minute, mock.contextDialer)
	if err != nil {
		log.Fatalf("Could not connect to services: %v", err)
	}

	recorderproxy.SetCA("", "")
	//spAddr := spUrl.Host
	spAddr := ""
	proxy := recorderproxy.NewRecorderProxy(0, conn, 1*time.Minute, spAddr)
	proxy.SetVerbose(viper.GetBool("verbose"))
	p := httptest.NewServer(proxy)
	proxyUrl, _ := url.Parse(p.URL)
	tr := &http.Transport{TLSClientConfig: acceptAllCerts, Proxy: http.ProxyURL(proxyUrl), DisableKeepAlives: true}
	client := &http.Client{Transport: tr}

	return client
}
