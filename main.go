package main

import (
	"fmt"
	"github.com/nlnwa/veidemann-recorderproxy/recorderproxy"
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
	verbose := flag.BoolP("verbose", "v", false, "should every proxy request be logged to stdout")
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
	flag.Parse()

	replacer := strings.NewReplacer("-", "_")
	viper.SetEnvKeyReplacer(replacer)
	viper.AutomaticEnv()
	viper.BindPFlags(flag.CommandLine)

	err := recorderproxy.SetCA(viper.GetString("ca"), viper.GetString("ca-key"))
	if err != nil {
		log.Fatal(err)
	}

	timeout := viper.GetDuration("timeout")
	contentWriterAddr := viper.GetString("content-writer-host") + ":" + viper.GetString("content-writer-port")
	dnsResolverAddr := viper.GetString("dns-resolver-host") + ":" + viper.GetString("dns-resolver-port")
	browserControllerAddr := viper.GetString("browser-controller-host") + ":" + viper.GetString("browser-controller-port")
	cacheAddr := viper.GetString("cache-host") + ":" + viper.GetString("cache-port")

	conn := recorderproxy.NewConnections()
	err = conn.Connect(contentWriterAddr, dnsResolverAddr, browserControllerAddr, timeout)
	if err != nil {
		log.Fatalf("Could not connect to services: %v", err)
	}

	fmt.Printf("Using cache at %s\n", cacheAddr)

	firstPort := viper.GetInt("port")
	proxyCount := viper.GetInt("proxy-count")
	for i := firstPort; i < (firstPort + proxyCount); i++ {
		r := recorderproxy.NewRecorderProxy(i, conn, timeout, cacheAddr)
		r.SetVerbose(*verbose)
		r.Start()
	}

	fmt.Printf("Veidemann recorder proxy started\n")
	fmt.Printf("Verbose: %t\n", *verbose)

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
