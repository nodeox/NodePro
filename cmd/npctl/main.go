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
		cmd  = flag.String("cmd", "status", "Command: status, config, metrics, logs, connections, kick, kick-user, init-config")
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
		id := flag.Arg(0)
		if id == "" {
			fmt.Println("Usage: npctl --cmd kick <session_id>")
			os.Exit(1)
		}
		kickSession(*addr, id)
	case "kick-user":
		userID := flag.Arg(0)
		if userID == "" {
			fmt.Println("Usage: npctl --cmd kick-user <user_id>")
			os.Exit(1)
		}
		kickUser(*addr, userID)
	case "init-config":
		generateInitConfig(*out)
	default:
		fmt.Printf("Unknown command: %s\n", *cmd)
		os.Exit(1)
	}
}

func kickSession(addr, id string) {
	resp, err := http.Post(addr+"/connections/close?id="+id, "application/json", nil)
	if err != nil { fmt.Printf("Error: %v\n", err); return }
	if resp.StatusCode == http.StatusNoContent {
		fmt.Printf("Session %s closed.\n", id)
	} else {
		fmt.Printf("Failed to close session: %s\n", resp.Status)
	}
}

func kickUser(addr, userID string) {
	resp, err := http.Post(addr+"/users/kick?user_id="+userID, "application/json", nil)
	if err != nil { fmt.Printf("Error: %v\n", err); return }
	body, _ := io.ReadAll(resp.Body)
	fmt.Print(string(body))
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

func callAPI(url string) {
	resp, err := http.Get(url)
	if err != nil { fmt.Printf("Error: %v\n", err); return }
	defer resp.Body.Close()
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
