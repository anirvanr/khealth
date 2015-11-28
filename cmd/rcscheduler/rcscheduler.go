package main

import (
	"flag"
	"github.com/coreos/khealth/pkg/collectors"
	"github.com/coreos/khealth/pkg/routines"
	"github.com/coreos/pkg/flagutil"
	kclient "k8s.io/kubernetes/pkg/client/unversioned"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	var listenAddr string
	var clientMode string
	var remoteConfig kclient.Config
	fs := flag.NewFlagSet("rcscheduler", flag.ExitOnError)
	fs.StringVar(&listenAddr, "listen", "0.0.0.0:8080", "bind http server to this address")
	fs.StringVar(&clientMode, "client-mode", "in-cluster", "mode by which this client is configured to talk to the k8s api: one of [ in-cluster, remote-tls, remote-basic-auth ]")

	fs.StringVar(&remoteConfig.Host, "remote-host", "", "host:port or url of k8s api server")
	fs.StringVar(&remoteConfig.Username, "remote-username", "", "basic auth username")
	fs.StringVar(&remoteConfig.Password, "remote-password", "", "basic auth password")
	fs.StringVar(&remoteConfig.CertFile, "remote-tls-cert-file", "", "path to tls cert file")
	fs.StringVar(&remoteConfig.KeyFile, "remote-tls-key-file", "", "path to tls key file")
	fs.StringVar(&remoteConfig.CAFile, "remote-tls-ca-file", "", "path to tls certificate authority file")

	var pollInterval, podTTL int

	fs.IntVar(&pollInterval, "poll-interval", 5, "number of seconds between kubernetes api status polls")
	fs.IntVar(&podTTL, "pod-ttl", 120, "number of seconds to leave canary pods running before destroying and re-creating")

	if err := fs.Parse(os.Args[1:]); err != nil {
		log.Fatal(err)
	}

	if err := flagutil.SetFlagsFromEnv(fs, "KHEALTH_RCSCHEDULER"); err != nil {
		log.Fatal(err)
	}

	var client *kclient.Client
	var err error

	//TODO: explicitly check necessary args for each clientMode before trying to create client
	switch clientMode {
	case "in-cluster":
		client, err = kclient.NewInCluster()
	case "remote-tls":
		client, err = kclient.New(&remoteConfig)
	case "remote-basic-auth":
		client, err = kclient.New(&remoteConfig)
	default:
		log.Fatalf("Invalid client-mode: %s\n", clientMode)
	}

	if err != nil {
		log.Fatalf("Error creating client: %s\n", err)
	}

	rcScheduler := routines.NewRoutine(
		client,
		time.Duration(pollInterval)*time.Second,
		time.Duration(podTTL)*time.Second,
		&routines.RCScheduler{
			Client:       client,
			Namespace:    "khealth",
			ReplicaCount: 3,
		},
	)

	collector := collectors.NewSimpleCollector(rcScheduler)

	if err := collector.Start(); err != nil {
		log.Fatal(err)
	}
	sigc := make(chan os.Signal, 1)

	signal.Notify(sigc,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)

	go func() {
		_ = <-sigc
		log.Println("Caught signal: attempting to terminate gracefully")
		if err := collector.Terminate(); err != nil {
			log.Fatal(err)
		}
		log.Println("Terminated")
		os.Exit(0)
	}()

	http.HandleFunc("/health", collector.Status)

	log.Fatal(http.ListenAndServe(listenAddr, nil))
}
