package gateway

import (
    "encoding/json"
    "testing"
)

// UAT #77: Verify the actual production MCP tools/list contract, not merely
// Go's fileMkdirInput type or a synthetic/mock schema. The optional for_shell
// input creates a new workspace as the policy-selected non-root account.
// If the Gateway omits it from the published catalog, ChatGPT cannot invoke
// the opt-in without unsafe manual chown.
func TestInfinityMkdirForShellAdvertisedInRealMCPCatalog(t *testing.T) {
    s := gateCTransportFixture(t)
    defer s.Close()

    cursor := ""
    found := false
    for page := 0; page < 20; page++ {
        params := map[string]any{}
        if cursor != "" { params["cursor"] = cursor }
        status, rpc := gateCProfileRPC(t, s.URL, "tools/list", params, 8800+page)
        if status != 200 || len(rpc["error"]) > 0 {
            t.Fatalf("tools/list HTTP=%d error=%s", status, rpc["error"])
        }
        var result struct {
            Tools []struct {
                Name string `json:"name"`
                InputSchema json.RawMessage `json:"inputSchema"`
            } `json:"tools"`
            NextCursor string `json:"nextCursor"`
        }
        if err := json.Unmarshal(rpc["result"], &result); err != nil { t.Fatal(err) }
        for _, tool := range result.Tools {
            if tool.Name != "file.mkdir" { continue }
            if found { t.Fatal("duplicate file.mkdir entry") }
            found = true

            var schema struct {
                Type string `json:"type"`
                Properties map[string]struct {
                    Type string `json:"type"`
                    Description string `json:"description"`
                } `json:"properties"`
                Required []string `json:"required"`
            }
            if err := json.Unmarshal(tool.InputSchema, &schema); err != nil { t.Fatal(err) }
            if schema.Type != "object" {
                t.Fatalf("file.mkdir input schema must be object: %s", tool.InputSchema)
            }
            if schema.Properties["path"].Type != "string" {
                t.Fatalf("file.mkdir path type lost: %s", tool.InputSchema)
            }
            forShell, present := schema.Properties["for_shell"]
            if !present || forShell.Type != "boolean" || forShell.Description == "" {
                t.Fatalf("file.mkdir for_shell boolean opt-in missing from published tools/list: %s", tool.InputSchema)
            }
            requiredPath := false
            for _, name := range schema.Required {
                if name == "for_shell" { t.Fatal("for_shell must remain optional for legacy callers") }
                if name == "path" { requiredPath = true }
            }
            if !requiredPath { t.Fatal("file.mkdir path must remain required") }
        }
        if result.NextCursor == "" { break }
        if result.NextCursor == cursor { t.Fatal("repeated tools/list cursor") }
        cursor = result.NextCursor
        if page == 19 { t.Fatal("tools/list catalog truncated") }
    }
    if !found { t.Fatal("file.mkdir not advertised by production Gateway") }
}
