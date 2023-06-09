package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/DataDog/datadog-go/statsd"
)

var (
	concurrency int
	maxMessages int
	resolver    string
	runForever  bool
	reqPerSec   int
	dnsProtocol string
	protocol    string
)

type DNSProtocol string

const (
	DNS          DNSProtocol = "dns"
	DNSOverHTTPS DNSProtocol = "doh"
)

func (d DNSProtocol) String() string {
	return string(d)
}

func (d DNSProtocol) Validate() error {
	switch d {
	case DNS, DNSOverHTTPS:
		return nil
	default:
		return fmt.Errorf("invalid protocol %s", d)
	}
}

func init() {
	flag.IntVar(&concurrency, "concurrency", runtime.NumCPU(),
		"Number of concurrent goroutines used for sending")
	flag.IntVar(&maxMessages, "m", 100000,
		"Maximum number of messages to send before stopping. Can be overridden to never stop with -inf")
	flag.IntVar(&reqPerSec, "t", 0,
		"Target request rate per second, defaults to unlimited")
	flag.StringVar(&dnsProtocol, "d", DNS.String(),
		"Type of DNS protocol to use, either 'dns' or 'doh' (default 'dns')")
	flag.StringVar(&resolver, "r", "127.0.0.1:53",
		"Resolver to test against")
	flag.StringVar(&protocol, "p", "udp",
		"Protocol to use, only applies if using DNS as protocol")
	flag.BoolVar(&runForever, "inf", false,
		"Run Forever")
}

func main() {
	fmt.Printf("dnsstress - dns stress tool\n\n")

	flag.Usage = func() {
		fmt.Fprint(os.Stderr, strings.Join([]string{
			"Send DNS requests as fast as possible to a given server and display the rate.",
			"",
			"Usage: dnsstress [option ...] targetdomain [targetdomain [...] ]",
			"",
		}, "\n"))
		flag.PrintDefaults()
	}

	flag.Parse()

	// We need at least one target domain
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	if concurrency < 1 {
		flag.Usage()
		os.Exit(1)
	}

	sdClient, err := statsd.New("127.0.0.1:8125")
	if err != nil {
		log.Fatal(err)
		return
	}
	defer sdClient.Close()

	if DNSProtocol(dnsProtocol).Validate() != nil {
		flag.Usage()
		os.Exit(1)
	}

	if !strings.Contains(resolver, ":") { // TODO: improve this test to make it work with IPv6 addresses
		// Automatically append the default port number if missing
		resolver = resolver + ":53"
	}

	// all remaining parameters are treated as domains to be used in round-robin in the threads
	targetDomains := make([]string, flag.NArg())
	for index, element := range flag.Args() {
		if element[len(element)-1] == '.' {
			targetDomains[index] = element
		} else {
			targetDomains[index] = element + "."
		}
	}

	if dnsProtocol == DNS.String() {
		protocol = strings.ToLower(protocol)
		switch protocol {
		case "udp":
		case "tcp":
		default:
			log.Fatalf("unknown protocol %s", protocol)
		}
	}

	fmt.Printf("Target domains: %v.\n", targetDomains)

	exit := make(chan struct{})
	go handleSignals(exit)

	if runForever {
		maxMessages = math.MaxInt64
	}
	dnsResolver := NewResolver(resolver, targetDomains[0], sdClient, ResolverOptions{
		Concurrency:       concurrency,
		MaxMessages:       maxMessages,
		RequestsPerSecond: reqPerSec,
		Protocol:          protocol,
		DNSProtocol:       DNSProtocol(dnsProtocol),
	})

	go func() {
		<-exit
		dnsResolver.Stop()
	}()
	dnsResolver.RunResolver()
}

func handleSignals(exit chan struct{}) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigs
	fmt.Printf("caught signal %s, stopping...\n", sig)
	close(exit)
}
