module github.com/nlnwa/veidemann-recorderproxy

require (
	github.com/dustin/go-broadcast v0.0.0-20171205050544-f664265f5a66
	github.com/elazarl/goproxy v0.0.0-20190421051319-9d40249d3c2f
	github.com/golang/protobuf v1.3.1
	github.com/grpc-ecosystem/grpc-gateway v1.8.5 // indirect
	github.com/nlnwa/veidemann-api-go v1.0.0-beta5
	github.com/sirupsen/logrus v1.4.1
	github.com/spf13/pflag v1.0.3
	github.com/spf13/viper v1.3.2
	golang.org/x/net v0.0.0-20190311183353-d8887717615a
	google.golang.org/appengine v1.4.0 // indirect
	google.golang.org/genproto v0.0.0-20190502173448-54afdca5d873 // indirect
	google.golang.org/grpc v1.21.0
)

replace github.com/elazarl/goproxy => ../../../github.com/elazarl/goproxy

replace github.com/nlnwa/veidemann-api-go => ../../../github.com/nlnwa/veidemann-api-go
