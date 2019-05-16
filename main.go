package main

import (
	"flag"
	"fmt"
	"github.com/nlnwa/veidemann-recorderproxy/recorderproxy"
	"log"
	"os"
	"os/signal"
)

func main() {
	verbose := flag.Bool("v", false, "should every proxy request be logged to stdout")
	firstPort := flag.Int("port", 8080, "first proxy listen port")
	proxyCount := flag.Int("num", 8080, "number of proxies to start")
	flag.Parse()
	err := setCA(caCert, caKey)
	if err != nil {
		log.Fatal(err)
	}

	//conn := test_util.NewConnectionsMock()
	conn := recorderproxy.NewConnections()
	err = conn.Connect("localhost:7777", "localhost:7778", "localhost:7779")
	if err != nil {
		log.Fatalf("Could not connect to services: %v", err)
	}

	for i := *firstPort; i < (*firstPort + *proxyCount); i++ {
		r := recorderproxy.NewRecorderProxy(i, conn)
		r.SetVerbose(*verbose)
		r.Start()
	}

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
