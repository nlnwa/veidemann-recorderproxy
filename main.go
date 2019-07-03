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

func main() {
	verbose := flag.BoolP("verbose", "v", false, "should every proxy request be logged to stdout")
	flag.Int("port", 8080, "first proxy listen port")
	flag.Int("proxy-count", 10, "number of proxies to start")
	flag.String("content-writer", "localhost:7777", "Content writer address (host:port)")
	flag.String("dns-resolver", "localhost:7778", "DNS resolver address (host:port)")
	flag.String("browser-controller", "localhost:7779", "Browser controller address (host:port)")
	flag.Duration("timeout", 10*time.Minute, "Timeout used for connecting to GRPC services")
	flag.String("ca", "", "Path to CA certificate used for signing client connections")
	flag.String("ca-key", "", "Path to private key for CA certificate used for signing client connections")
	flag.String("cache", "", "Cache address (host:port)")
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
	conn := recorderproxy.NewConnections()
	err = conn.Connect(viper.GetString("content-writer"), viper.GetString("dns-resolver"), viper.GetString("browser-controller"), timeout)
	if err != nil {
		log.Fatalf("Could not connect to services: %v", err)
	}

	cache := viper.GetString("cache")
	fmt.Printf("Using cache at %s\n", cache)

	firstPort := viper.GetInt("port")
	proxyCount := viper.GetInt("proxy-count")
	for i := firstPort; i < (firstPort + proxyCount); i++ {
		r := recorderproxy.NewRecorderProxy(i, conn, timeout, cache)
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
