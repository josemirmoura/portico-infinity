package broker

import (
    "context"
    "errors"
    "regexp"
    "strconv"
    "strings"
    "time"

    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/hostexec"
)

// A Pórtico Infinity Broker is a container: its /etc/passwd is NOT the host's
// account database. The shell runner uses host systemd via hostexec, so the
// workspace owner must be resolved in that same host identity namespace.
// Never infer ownership from container-local os/user.Lookup or user-controlled
// UID/GID arguments. This helper only reads an account named in root-owned
// shell.run_as policy; it never mutates host accounts.
var shellAccountName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.-]*package broker

import (
    "context"
    "errors"
    "regexp"
    "strconv"
    "strings"
    "time"

    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/hostexec"
)

// A Pórtico Infinity Broker is a container: its /etc/passwd is NOT the host's
// account database. The shell runner uses host systemd via hostexec, so the
// workspace owner must be resolved in that same host identity namespace.
// Never infer ownership from container-local os/user.Lookup or user-controlled
// UID/GID arguments. This helper only reads an account named in root-owned
// shell.run_as policy; it never mutates host accounts.
)

func resolveHostShellAccount(ctx context.Context, runAs string) (int, int, error) {
    if len(runAs) > 128 || !shellAccountName.MatchString(runAs) {
        return 0, 0, errors.New("invalid configured shell account name")
    }
    lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()

    // hostexec uses the already-approved host namespace mechanism when the
    // Infinity Broker runs in Docker. No shell, no password contents logged,
    // and the account name is not supplied by the MCP caller.
    data, err := hostexec.CommandContext(lookupCtx, "getent", "passwd", runAs).Output()
    if err != nil || len(data) == 0 || len(data) > 4096 {
        return 0, 0, errors.New("host account lookup unavailable")
    }
    return parseHostShellAccountRecord(runAs, data)
}

// Keep validation independent of NSS, so hostile or malformed responses can
// be regression-tested without running any process as root.
func parseHostShellAccountRecord(runAs string, data []byte) (int, int, error) {
    if len(data) == 0 || len(data) > 4096 {
        return 0, 0, errors.New("host shell account response size invalid")
    }
    record := strings.TrimSuffix(string(data), "\n")
    fields := strings.Split(record, ":")
    if len(fields) != 7 || fields[0] != runAs ||
        strings.ContainsAny(record, "\r\n\x00") {
        return 0, 0, errors.New("invalid host account lookup response")
    }
    uid, err := strconv.Atoi(fields[2])
    if err != nil || uid <= 0 {
        return 0, 0, errors.New("host shell account must be non-root")
    }
    gid, err := strconv.Atoi(fields[3])
    if err != nil || gid < 0 {
        return 0, 0, errors.New("invalid host shell account group")
    }
    return uid, gid, nil
}
