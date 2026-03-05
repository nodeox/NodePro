package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/nodeox/NodePro/internal/common"
	"github.com/nodeox/NodePro/internal/routing"
	"gopkg.in/yaml.v3"
)

func main() {
	var (
		addr = flag.String("addr", "http://127.0.0.1:8081", "Agent Admin API address")
		cmd  = flag.String("cmd", "status", "Command: status, config, metrics, logs, connections, kick, kick-user, dns-flush, quota-reset, init-config")
		id   = flag.String("id", "", "ID for kick, kick-user, or quota-reset")
		out  = flag.String("out", "configs/auto_ingress.yaml", "Output path for init-config")
	)
	flag.Parse()

	switch *cmd {
	case "status":
		callAPI(*addr + "/status")
	case "config":
		callAPI(*addr + "/config")
	case "metrics":
		callAPI(*addr + "/metrics")
	case "logs":
		callAPI(*addr + "/logs")
	case "connections":
		callAPI(*addr + "/connections")
	case "kick":
		if *id == "" { fmt.Println("Usage: npctl --cmd kick --id <session_id>"); os.Exit(1) }
		postAPI(*addr + "/connections/close?id=" + *id)
	case "kick-user":
		if *id == "" { fmt.Println("Usage: npctl --cmd kick-user --id <user_id>"); os.Exit(1) }
		postAPI(*addr + "/users/kick?user_id=" + *id)
	case "dns-flush":
		postAPI(*addr + "/dns/flush")
	case "quota-reset":
		if *id == "" { fmt.Println("Usage: npctl --cmd quota-reset --id <user_id>"); os.Exit(1) }
		postAPI(*addr + "/users/quota/reset?user_id=" + *id)
	case "init-config":
		generateInitConfig(*out)
	default:
		fmt.Printf("Unknown command: %s\n", *cmd)
		os.Exit(1)
	}
}

func callAPI(url string) {
	resp, err := http.Get(url)
	if err != nil { fmt.Printf("Error: %v\n", err); return }
	defer resp.Body.Close()
	printResp(resp)
}

func postAPI(url string) {
	resp, err := http.Post(url, "application/json", nil)
	if err != nil { fmt.Printf("Error: %v\n", err); return }
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		fmt.Println("Success")
	} else {
		printResp(resp)
	}
}

func printResp(resp *http.Response) {
	body, _ := io.ReadAll(resp.Body)
	if resp.Header.Get("Content-Type") == "application/json" {
		var out interface{}
		json.Unmarshal(body, &out)
		pretty, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(pretty))
	} else {
		fmt.Println(string(body))
	}
}

func generateInitConfig(path string) {
	rules := routing.GenerateDefaultSplitConfig("proxy-node")
	var ruleCfgs []common.RoutingRuleConfig
	for _, r := range rules {
		ruleCfgs = append(ruleCfgs, common.RoutingRuleConfig{
			Type: r.Type, Pattern: r.Pattern, Outbound: r.Outbound,
		})
	}
	cfg := &common.Config{
		Version: "2.0",
		Node:    common.NodeConfig{ID: "agent-auto", Type: "ingress"},
		Inbounds: []common.InboundConfig{
			{Protocol: "socks5", Listen: "127.0.0.1:1080", Auth: common.AuthConfig{Enabled: false}},
		},
		Outbounds: []common.OutboundConfig{
			{Name: "direct", Protocol: "direct", Group: "default"},
			{Name: "proxy-node", Protocol: "np-chain", Address: "YOUR_RELAY_IP:443", Transport: "quic"},
		},
		Routing: common.RoutingConfig{Rules: ruleCfgs},
		Observability: common.ObservabilityConfig{
			Metrics: common.MetricsConfig{Enabled: true, Listen: "0.0.0.0:9090"},
			Logging: common.LoggingConfig{Level: "info", Format: "console"},
		},
	}
	data, _ := yaml.Marshal(cfg)
	os.WriteFile(path, data, 0644)
	fmt.Printf("Default configuration generated at: %s\n", path)
}
