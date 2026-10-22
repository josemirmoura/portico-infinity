package broker

import (
    "context"
    "strings"
    "testing"

    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/wire"
)

func TestFilecopyIdentityIsStableButSubjectScoped(t *testing.T) {
    a := filecopyID("user-a", "stable-id")
    b := filecopyID("user-a", "stable-id")
    c := filecopyID("user-b", "stable-id")
    if a != b || a == c || !strings.HasPrefix(a, "vps-agent-filecopy-") {
        t.Fatal("job identity must be stable and principal-specific")
    }
}

func TestFilecopyDisabledWithoutIndependentOperatorOptIn(t *testing.T) {
    t.Setenv("VPS_AGENT_INFINITY_PROTECTED_ROOT", "/srv/portico-infinity")
    t.Setenv("VPS_AGENT_INFINITY_FILECOPY_JOBS", "0")
    t.Setenv("VPS_AGENT_INFINITY_FILECOPY_MAX_BYTES", "1099511627776")
    req := wire.Request{ID:"no-worker",Subject:"operator",Tool:"file.copy.start",
        Resource:"/opt/models/a.gguf",InvocationID:"untrusted-operation"}
    b:=&Broker{}
    resp:=b.startFilecopy(context.Background(),req)
    if resp.OK || resp.Error == nil || resp.Error.Code!="permission_denied" {
        t.Fatalf("filecopy must be disabled, got %#v",resp)
    }
    t.Setenv("VPS_AGENT_INFINITY_FILECOPY_JOBS", "1")
    t.Setenv("VPS_AGENT_INFINITY_PROTECTED_ROOT", "")
    if filecopyOperatorQuota() != 0 { t.Fatal("legacy Community must never enable filecopy worker") }
    t.Setenv("VPS_AGENT_INFINITY_PROTECTED_ROOT", "/srv/portico-infinity")
    t.Setenv("VPS_AGENT_INFINITY_FILECOPY_MAX_BYTES", "0")
    if filecopyOperatorQuota() != 0 { t.Fatal("zero quota must disable worker") }
    t.Setenv("VPS_AGENT_INFINITY_FILECOPY_MAX_BYTES", "1099511627777")
    if filecopyOperatorQuota() != 0 { t.Fatal("oversized quota must disable worker") }
}

func TestWorkerUnitIDRequiresExactPrefixAndHex(t *testing.T) {
    a:=filecopyID("alice","op")
    for _, bad := range []string{"/", "portico-infinity.service", "vps-agent-filecopy-123",
        "vps-agent-compose-"+strings.Repeat("a",32)} {
        if a==bad { t.Fatal("unrelated host service identity accepted") }
    }
}
