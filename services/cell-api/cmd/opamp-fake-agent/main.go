// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Command opamp-fake-agent connects simulated OpAMP agents to a cell, for
// end-to-end tests and load tests of collector management. Each agent
// describes itself like a collector, reports health and an effective
// configuration, and sends heartbeats; it runs no pipeline.
//
//	opamp-fake-agent -url http://localhost:8081/api/v1/opamp -token sct_... -n 1
//	opamp-fake-agent -url ... -token ... -n 500 -http-share 0.3 -for 5m
//
// The agents keep running until -for elapses or the process is stopped.
// With -once the command exits as soon as every agent has delivered its
// first message, which is what a test that only needs the registry
// filled wants.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/open-telemetry/opamp-go/client"
	"github.com/open-telemetry/opamp-go/client/types"
	"github.com/open-telemetry/opamp-go/protobufs"
)

func main() {
	endpoint := flag.String("url", "http://localhost:8081/api/v1/opamp", "the cell's OpAMP endpoint (http(s); WebSocket agents use ws(s))")
	token := flag.String("token", os.Getenv("SLUICIO_COLLECTOR_TOKEN"), "team token (or SLUICIO_COLLECTOR_TOKEN)")
	n := flag.Int("n", 1, "number of agents")
	httpShare := flag.Float64("http-share", 0, "share of agents on plain HTTP polling, 0 to 1")
	hostPrefix := flag.String("host", "fake-agent", "host.name prefix; agent i is <host>-<i>")
	configFile := flag.String("config", "", "YAML file every agent reports as its effective configuration (default: a small built-in one)")
	supervisor := flag.Bool("supervisor", false, "report accepts_remote_config, like the supervisor type")
	uidSeed := flag.String("uid-seed", "", "derive stable instance UIDs from this seed, so a rerun is the same agents")
	heartbeat := flag.Duration("heartbeat", 30*time.Second, "heartbeat interval")
	poll := flag.Duration("poll", 30*time.Second, "polling interval for HTTP agents")
	insecure := flag.Bool("insecure", false, "skip TLS verification (a test cell's own certificate)")
	runFor := flag.Duration("for", 0, "stop after this long (0: until interrupted)")
	once := flag.Bool("once", false, "exit once every agent has delivered its first message")
	verbose := flag.Bool("v", false, "log every failed connection attempt")
	flag.Parse()
	if *token == "" {
		log.Fatal("a team token is required (-token or SLUICIO_COLLECTOR_TOKEN)")
	}
	config := defaultConfig
	if *configFile != "" {
		b, err := os.ReadFile(*configFile)
		if err != nil {
			log.Fatal(err)
		}
		config = string(b)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if *runFor > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *runFor)
		defer cancel()
	}

	caps := protobufs.AgentCapabilities_AgentCapabilities_ReportsStatus |
		protobufs.AgentCapabilities_AgentCapabilities_ReportsEffectiveConfig |
		protobufs.AgentCapabilities_AgentCapabilities_ReportsHealth |
		protobufs.AgentCapabilities_AgentCapabilities_ReportsHeartbeat
	if *supervisor {
		caps |= protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig
	}

	var connected, failed atomic.Int64
	var first sync.WaitGroup
	clients := make([]client.OpAMPClient, 0, *n)
	httpAgents := int(float64(*n) * *httpShare)
	start := time.Now()
	for i := 0; i < *n; i++ {
		ws := i >= httpAgents
		var c client.OpAMPClient
		url := *endpoint
		if ws {
			c = client.NewWebSocket(nil)
			url = strings.Replace(strings.Replace(url, "https://", "wss://", 1), "http://", "ws://", 1)
		} else {
			hc := client.NewHTTP(nil)
			hc.SetPollingInterval(*poll)
			c = hc
		}
		name := fmt.Sprintf("%s-%d", *hostPrefix, i)
		uid := uuid.New()
		if *uidSeed != "" {
			uid = uuid.NewSHA1(uuid.NameSpaceOID, []byte(*uidSeed+"/"+name))
		}
		_ = c.SetAgentDescription(&protobufs.AgentDescription{
			IdentifyingAttributes: []*protobufs.KeyValue{
				kv("service.name", "otelcol-contrib"), kv("service.version", "0.166.0"), kv("service.instance.id", uid.String()),
			},
			NonIdentifyingAttributes: []*protobufs.KeyValue{kv("host.name", name), kv("os.type", "linux")},
		})
		_ = c.SetHealth(&protobufs.ComponentHealth{Healthy: true, Status: "StatusOK", StartTimeUnixNano: uint64(time.Now().UnixNano())})
		var once sync.Once
		first.Add(1)
		hb := *heartbeat
		// opamp-go speaks TLS whenever a TLS config is set, even to ws://,
		// so only a secure URL gets one.
		var tlsCfg *tls.Config
		if strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "wss://") {
			tlsCfg = &tls.Config{InsecureSkipVerify: *insecure} //nolint:gosec // opt-in, for a test cell's own certificate
		}
		err := c.Start(ctx, types.StartSettings{
			OpAMPServerURL:    url,
			Header:            http.Header{"Authorization": []string{"Bearer " + *token}},
			TLSConfig:         tlsCfg,
			InstanceUid:       types.InstanceUid(uid),
			Capabilities:      caps,
			HeartbeatInterval: &hb,
			Callbacks: types.Callbacks{
				OnConnect: func(context.Context) {
					connected.Add(1)
					once.Do(first.Done)
				},
				OnConnectFailed: func(_ context.Context, err error) {
					failed.Add(1)
					if *verbose {
						log.Printf("%s: %v", name, err)
					}
				},
				GetEffectiveConfig: func(context.Context) (*protobufs.EffectiveConfig, error) {
					return &protobufs.EffectiveConfig{ConfigMap: &protobufs.AgentConfigMap{ConfigMap: map[string]*protobufs.AgentConfigObject{
						"": {Body: []byte(config), ContentType: "text/yaml"},
					}}}, nil
				},
			},
		})
		if err != nil {
			log.Fatalf("agent %d: %v", i, err)
		}
		clients = append(clients, c)
	}

	done := make(chan struct{})
	go func() { first.Wait(); close(done) }()
	select {
	case <-done:
		log.Printf("all %d agents connected in %s (%d WebSocket, %d HTTP); %d failed attempts", *n, time.Since(start).Round(time.Millisecond), *n-httpAgents, httpAgents, failed.Load())
	case <-ctx.Done():
		log.Printf("stopped before every agent connected: %d connects, %d failed attempts", connected.Load(), failed.Load())
	}
	if !*once {
		<-ctx.Done()
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, c := range clients {
		_ = c.Stop(stopCtx)
	}
	select {
	case <-done:
	default:
		os.Exit(1)
	}
}

func kv(k, v string) *protobufs.KeyValue {
	return &protobufs.KeyValue{Key: k, Value: &protobufs.AnyValue{Value: &protobufs.AnyValue_StringValue{StringValue: v}}}
}

const defaultConfig = `receivers:
  otlp:
    protocols:
      http:
        endpoint: 0.0.0.0:4318
processors:
  batch: {}
exporters:
  otlp_http/sluicio:
    endpoint: https://ingest.example.test
    headers:
      authorization: Bearer fake-agent-secret
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlp_http/sluicio]
`
