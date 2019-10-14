module github.com/nlnwa/veidemann-recorderproxy

require (
	github.com/codahale/hdrhistogram v0.0.0-20161010025455-3a0bb77429bd // indirect
	github.com/felixge/httpsnoop v1.0.1 // indirect
	github.com/getlantern/errors v0.0.0-20190325191628-abdb3e3e36f7
	github.com/getlantern/golog v0.0.0-20190830074920-4ef2e798c2d7 // indirect
	github.com/getlantern/lampshade v0.0.0-20190821034142-fbb277326291 // indirect
	github.com/getlantern/mitm v0.0.0-20180205214248-4ce456bae650
	github.com/getlantern/proxy v0.0.0-20190225163220-31d1cc06ed3d
	github.com/go-test/deep v1.0.4
	github.com/golang/protobuf v1.3.2
	github.com/grpc-ecosystem/go-grpc-middleware v1.1.0
	github.com/grpc-ecosystem/grpc-gateway v1.9.2 // indirect
	github.com/nlnwa/veidemann-api-go v1.0.0-beta10
	github.com/opentracing-contrib/go-stdlib v0.0.0-20190519235532-cf7a6c988dc9
	github.com/opentracing/opentracing-go v1.1.0
	github.com/pkg/errors v0.8.1
	github.com/sirupsen/logrus v1.4.2
	github.com/spf13/pflag v1.0.3
	github.com/spf13/viper v1.3.2
	github.com/uber-go/atomic v1.4.0 // indirect
	github.com/uber/jaeger-client-go v2.17.0+incompatible
	github.com/uber/jaeger-lib v2.1.1+incompatible // indirect
	golang.org/x/net v0.0.0-20190912160710-24e19bdeb0f2
	golang.org/x/sys v0.0.0-20190902133755-9109b7679e13 // indirect
	google.golang.org/grpc v1.23.1
)

replace github.com/getlantern/proxy => github.com/nlnwa/getlantern-proxy v0.0.0-20191010083338-hooks
