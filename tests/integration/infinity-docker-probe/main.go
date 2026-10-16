// E2E client: real MCP over loopback to disposable Docker Gateway and Broker.
// Never direct this binary at a production host.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c := req.Clone(req.Context())
	c.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(c)
}

type result struct {
	Case    string `json:"case"`
	Passed  bool   `json:"passed"`
	Detail  string `json:"detail,omitempty"`
}

func main() {
	endpoint := flag.String("endpoint", "http://127.0.0.1:18081/mcp", "local disposable endpoint")
	directory := flag.String("directory", "", "disposable directory below /opt/infinity-e2e-*")
	output := flag.String("output", "", "JSON result file")
	flag.Parse()
	token := os.Getenv("INFINITY_E2E_BEARER_TOKEN")
	if token == "" || !strings.HasPrefix(*endpoint, "http://127.0.0.1:") ||
		!strings.HasPrefix(*directory, "/opt/infinity-e2e-") || *output == "" {
		fmt.Fprintln(os.Stderr, "E2E must use local 127.0.0.1, an /opt/infinity-e2e-* directory and a lab token")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "infinity-docker-e2e", Version: "v1"}, nil)
	tr := &mcp.StreamableClientTransport{
		Endpoint: *endpoint,
		HTTPClient: &http.Client{Transport: bearerTransport{token: token}, Timeout: 15 * time.Second},
	}
	session, err := client.Connect(ctx, tr, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "real Gateway connection failed: %v\n", err)
		os.Exit(1)
	}
	defer session.Close()
	results := []result{}
	fail := false
	add := func(name string, allowed bool, args map[string]any) {
		resp, callErr := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		passed := callErr == nil && resp != nil && resp.IsError != allowed
		r := result{Case: name, Passed: passed}
		if callErr != nil {
			r.Detail = callErr.Error()
		} else if resp != nil && resp.IsError != allowed {
			r.Detail = fmt.Sprintf("unexpected IsError=%t", resp.IsError)
		}
		results = append(results, r)
		if !passed {
			fail = true
		}
	}
	target := filepath.Join(*directory, "real-broker.txt")
	add("system.info", true, map[string]any{})
	add("file.write", true, map[string]any{"path": target, "content": "Pórtico Infinity E2E"})
	add("file.read", true, map[string]any{"path": target})
	add("file.chmod", true, map[string]any{"path": target, "mode": 384}) // 0600
	add("file.chown", true, map[string]any{"path": target, "uid": os.Getuid(), "gid": os.Getgid()})
	add("file.patch", true, map[string]any{"path": target, "old_text": "E2E", "new_text": "LAB"})
	add("file.write", false, map[string]any{"path": "/srv/portico-infinity/.env", "content": "ESCAPE"})
	add("file.chown", false, map[string]any{"path": "/srv/portico-infinity/config/policy.yaml", "uid": 0, "gid": 0})
	add("file.write", false, map[string]any{"path": "/etc/portico-infinity-escape.txt", "content": "ESCAPE"})
	add("file.write", false, map[string]any{"path": filepath.Join(*directory, "..", "..", "srv", "portico-infinity", "escape"), "content": "ESCAPE"})
	add("package.install", false, map[string]any{"name": "curl"})
	add("shell.exec_admin", false, map[string]any{"command": "id", "cwd": *directory, "grant_id": "invalid"})
	add("service.restart", false, map[string]any{"name": "docker.service"})
	add("docker.restart", false, map[string]any{"name": "portico-infinity-broker-1"})
	add("file.remove", true, map[string]any{"path": target})
	// A wrong token must not establish a live MCP session.
	badTr := &mcp.StreamableClientTransport{
		Endpoint: *endpoint,
		HTTPClient: &http.Client{Transport: bearerTransport{token: "intentionally-invalid-token"}, Timeout: 5 * time.Second},
	}
	badClient := mcp.NewClient(&mcp.Implementation{Name: "invalid-token-test", Version: "v1"}, nil)
	badSession, badErr := badClient.Connect(ctx, badTr, nil)
	if badSession != nil {
		_ = badSession.Close()
	}
	wrongTokenDenied := badErr != nil
	results = append(results, result{Case: "invalid bearer denied", Passed: wrongTokenDenied})
	fail = fail || !wrongTokenDenied

	encoded, err := json.MarshalIndent(map[string]any{
		"mode": "disposable-real-docker", "endpoint": *endpoint,
		"cases": results, "passed": !fail,
	}, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, errors.New("cannot encode evidence"))
		os.Exit(1)
	}
	if err := os.WriteFile(*output, encoded, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, r := range results {
		marker := "PASS"
		if !r.Passed {
			marker = "FAIL"
		}
		fmt.Printf("%s: %s %s\n", marker, r.Case, r.Detail)
	}
	if fail {
		os.Exit(1)
	}
}
