// E2E client: real MCP over loopback to disposable Docker Gateway and Broker.
// Never direct this binary at a production host.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"os/user"
	"strconv"
	"strings"
	"syscall"
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
	lab, err := os.OpenRoot(*directory)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot open disposable E2E root: %v\\n", err)
		os.Exit(1)
	}
	defer lab.Close()
	readLabProof := func(name string) (string, error) {
		file, openErr := lab.Open(name)
		if openErr != nil { return "", openErr }
		defer file.Close()
		bytes, readErr := io.ReadAll(io.LimitReader(file, 256))
		return string(bytes), readErr
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
		// Verify the real, registered MCP tool returns an in-band denial.
		// An unknown tool is a test bug, not evidence of policy enforcement.
		r := result{Case: name, Passed: passed}
		if !passed {
			if callErr != nil {
				r.Detail = callErr.Error()
			} else if resp != nil {
				r.Detail = fmt.Sprintf("unexpected IsError=%t", resp.IsError)
			} else {
				r.Detail = "missing MCP tool response"
			}
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
	// The shell must start a real non-root transient systemd job. Verify a
	// durable filesystem result, not only a successful MCP job-creation reply.
	add("shell.exec", true, map[string]any{
		"command": "printf shell-ok > shell-proof.txt", "cwd": *directory,
		"runtime_seconds": 20,
	})
	shellPassed := false
	for attempt := 0; attempt < 40; attempt++ {
		if data, readErr := readLabProof("shell-proof.txt"); readErr == nil && data == "shell-ok" {
			shellPassed = true
			break
		}
		select {
		case <-ctx.Done():
			break
		case <-time.After(500 * time.Millisecond):
		}
	}
	results = append(results, result{Case: "shell.exec real non-root workspace proof", Passed: shellPassed})
	fail = fail || !shellPassed
	// Regression: the Broker image deliberately has no portico-infinity-exec
	// account in its container-local passwd. The policy account exists ONLY
	// on the disposable host. A successful for_shell mkdir proves that
	// ownership came from the host namespace, not os/user.Lookup in Docker.
	workspace := filepath.Join(*directory, "for-shell-workspace")
	add("file.mkdir", true, map[string]any{"path": workspace, "for_shell": true})
	hostAccount, hostErr := user.Lookup("portico-infinity-exec")
	workspaceInfo, statErr := os.Stat(workspace)
	ownerProven := false
	if hostErr == nil && statErr == nil {
		wantUID, uidErr := strconv.ParseUint(hostAccount.Uid, 10, 32)
		wantGID, gidErr := strconv.ParseUint(hostAccount.Gid, 10, 32)
		actual, ok := workspaceInfo.Sys().(*syscall.Stat_t)
		ownerProven = uidErr == nil && gidErr == nil && ok &&
			uint64(actual.Uid) == wantUID && uint64(actual.Gid) == wantGID &&
			workspaceInfo.Mode().Perm() == 0750
	}
	results = append(results, result{Case: "for_shell workspace owned by host non-root account", Passed: ownerProven})
	fail = fail || !ownerProven
	add("file.mkdir", false, map[string]any{"path": workspace, "for_shell": true})
	add("file.mkdir", false, map[string]any{"path": "/opt", "for_shell": true})
	add("shell.exec", true, map[string]any{
		"command": "printf nonroot-for-shell-ok > for-shell-proof.txt",
		"cwd": workspace, "runtime_seconds": 20,
	})
	proofPath := filepath.Join(workspace, "for-shell-proof.txt")
	forShellWrite := false
	for attempt := 0; attempt < 40; attempt++ {
		resp, callErr := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "file.read", Arguments: map[string]any{"path": proofPath},
		})
		if callErr == nil && resp != nil && !resp.IsError {
			payload, encodeErr := json.Marshal(resp)
			if encodeErr == nil && strings.Contains(string(payload), "nonroot-for-shell-ok") {
				forShellWrite = true
				break
			}
		}
		select {
		case <-ctx.Done():
			break
		case <-time.After(500 * time.Millisecond):
		}
	}
	results = append(results, result{Case: "for_shell workspace real host-systemd write", Passed: forShellWrite})
	fail = fail || !forShellWrite
	if forShellWrite {
		add("file.remove", true, map[string]any{"path": proofPath})
	}
	if ownerProven && forShellWrite {
		add("file.remove", true, map[string]any{"path": workspace})
	}
	// The .env fixture is intentionally Unix-readable; a successful DENIED
	// result therefore proves the systemd mask rather than mode bits alone.
	add("shell.exec", true, map[string]any{
		"command": "if cat .env >/dev/null 2>&1; then printf LEAKED > secret-access.txt; else printf DENIED > secret-access.txt; fi",
		"cwd": *directory, "runtime_seconds": 20,
	})
	secretDenied := false
	for attempt := 0; attempt < 40; attempt++ {
		if data, readErr := readLabProof("secret-access.txt"); readErr == nil {
			secretDenied = data == "DENIED"
			break
		}
		select {
		case <-ctx.Done():
			break
		case <-time.After(500 * time.Millisecond):
		}
	}
	results = append(results, result{Case: "shell.exec blocks Unix-readable .env", Passed: secretDenied})
	fail = fail || !secretDenied
	add("shell.exec", false, map[string]any{
		"command": "id -u", "cwd": "/opt", "runtime_seconds": 20,
	})
	add("shell.exec_admin", false, map[string]any{"command": "id", "cwd": *directory, "grant_id": "invalid"})
	add("service.restart", false, map[string]any{"name": "docker.service"})
	add("docker.action", false, map[string]any{"name": "portico-infinity-broker-1", "action": "restart"})
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
