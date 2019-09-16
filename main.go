package main

import (
	"fmt"
	"github.com/nlnwa/veidemann-recorderproxy/recorderproxy"
	"github.com/nlnwa/veidemann-recorderproxy/tracing"
	"github.com/opentracing/opentracing-go"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"
)

func init() {
	os.Setenv("GODEBUG", os.Getenv("GODEBUG")+",tls13=1")
}

func main() {
	flag.Int("port", 8080, "first proxy listen port")
	flag.Int("proxy-count", 10, "number of proxies to start")
	flag.String("content-writer-host", "localhost", "Content writer host")
	flag.String("content-writer-port", "7777", "Content writer port")
	flag.String("dns-resolver-host", "localhost", "DNS resolver host")
	flag.String("dns-resolver-port", "7778", "DNS resolver port")
	flag.String("browser-controller-host", "localhost", "Browser controller host")
	flag.String("browser-controller-port", "7779", "Browser controller port")
	flag.Duration("timeout", 10*time.Minute, "Timeout used for connecting to GRPC services")
	flag.String("ca", "", "Path to CA certificate used for signing client connections")
	flag.String("ca-key", "", "Path to private key for CA certificate used for signing client connections")
	flag.String("cache-host", "", "Cache host")
	flag.String("cache-port", "", "Cache port")
	flag.String("log-level", "info", "log level, available levels are panic, fatal, error, warn, info, debug and trace")
	flag.String("log-formatter", "text", "log formatter, available values are text, logfmt and json")
	flag.Bool("log-method", false, "log method name")
	flag.Parse()

	replacer := strings.NewReplacer("-", "_")
	viper.SetEnvKeyReplacer(replacer)
	viper.AutomaticEnv()
	viper.BindPFlags(flag.CommandLine)

	recorderproxy.InitLog(viper.GetString("log-level"), viper.GetString("log-formatter"), viper.GetBool("log-method"))

	tracer, closer := tracing.Init("Recorder Proxy")
	opentracing.SetGlobalTracer(tracer)
	defer closer.Close()

	err := recorderproxy.SetCA(viper.GetString("ca"), viper.GetString("ca-key"))
	if err != nil {
		log.Fatal(err)
	}

	timeout := viper.GetDuration("timeout")
	cacheAddr := viper.GetString("cache-host") + ":" + viper.GetString("cache-port")

	conn := recorderproxy.NewConnections()
	err = conn.Connect(viper.GetString("content-writer-host"), viper.GetString("content-writer-port"),
		viper.GetString("dns-resolver-host"), viper.GetString("dns-resolver-port"),
		viper.GetString("browser-controller-host"), viper.GetString("browser-controller-port"),
		timeout)

	if err != nil {
		log.Fatalf("Could not connect to services: %v", err)
	}

	fmt.Printf("Using cache at %s\n", cacheAddr)

	firstPort := viper.GetInt("port")
	proxyCount := viper.GetInt("proxy-count")
	for i := firstPort; i < (firstPort + proxyCount); i++ {
		r := recorderproxy.NewRecorderProxy(i, conn, timeout, cacheAddr)
		r.Start()
	}

	fmt.Printf("Veidemann recorder proxy started\n")

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	func() {
		for sig := range c {
			// sig is a ^C, handle it
			fmt.Printf("SIG: %v\n", sig)
			return
		}
	}()
}
