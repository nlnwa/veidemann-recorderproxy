module github.com/nlnwa/veidemann-recorderproxy

go 1.13

require (
	github.com/codahale/hdrhistogram v0.0.0-20161010025455-3a0bb77429bd // indirect
	github.com/felixge/httpsnoop v1.0.1 // indirect
	github.com/getlantern/errors v0.0.0-20190325191628-abdb3e3e36f7
	github.com/getlantern/mitm v0.0.0-20180205214248-4ce456bae650
	github.com/getlantern/proxy v0.0.0-20190225163220-31d1cc06ed3d
	github.com/go-test/deep v1.0.4
	github.com/golang/protobuf v1.4.0-rc.4
	github.com/grpc-ecosystem/go-grpc-middleware v1.1.0
	github.com/kr/pretty v0.1.0 // indirect
	github.com/nlnwa/veidemann-api-go v1.0.0-beta13
	github.com/opentracing-contrib/go-stdlib v0.0.0-20190519235532-cf7a6c988dc9
	github.com/opentracing/opentracing-go v1.1.0
	github.com/pkg/errors v0.8.1
	github.com/sirupsen/logrus v1.4.2
	github.com/spf13/pflag v1.0.3
	github.com/spf13/viper v1.3.2
	github.com/uber-go/atomic v1.4.0 // indirect
	github.com/uber/jaeger-client-go v2.17.0+incompatible
	github.com/uber/jaeger-lib v2.1.1+incompatible // indirect
	golang.org/x/net v0.0.0-20200301022130-244492dfa37a
	google.golang.org/grpc v1.28.0
	google.golang.org/protobuf v1.20.1
	gopkg.in/check.v1 v1.0.0-20180628173108-788fd7840127 // indirect
)

replace github.com/getlantern/proxy => github.com/nlnwa/getlantern-proxy v0.0.0-20200423172401-8a11ded0a930
