# Infinity VPS edge bridge (standalone, no changes to production Traefik)

The VPS already runs Traefik v2.11 on network `traefik-public`, with Docker
provider and `websecure` / `letsencrypt`. The global Traefik is **not** edited
or restarted by this approach.

This Compose project creates only a dedicated non-root Traefik instance. The
existing Traefik sees its `traefik.http.routers.portico-infinity-public`
labels, terminates HTTPS for a **separate** domain, then sends that domain's
requests to this bridge over the existing Docker network. The bridge has a
**file provider of its own**, no Docker socket and no published host ports.
It dispatches requests to the three SRV-IA backends over WireGuard.

| URI | Backend |
| --- | --- |
| `/mcp`, `/.well-known/oauth-protected-resource`, `/healthz` | `10.88.88.2:18081`, HTTP |
| `/oauth/v2`, `/oidc/v1`, `/v2`, OIDC discovery | `10.88.88.2:18082`, **h2c** |
| `/ui/v2/login`, `/` | `10.88.88.2:18083`, HTTP |

**Do not deploy until SRV-IA Gateway and ZITADEL backends respond over
WireGuard and are bound ONLY to `10.88.88.2`.** Do not publish these
backends on `0.0.0.0`, the home Wi-Fi address or the Docker public bridge.

## VPS stage (operator only, later)

Checkout or copy this directory to `/opt/portico-infinity-edge`, with
root-owned configuration files. The bridge will be deployed independently,
under project name `portico-infinity-edge`:

```bash
cd /opt/portico-infinity-edge
export INFINITY_PUBLIC_DOMAIN=infinity.josemirmoura.com.br
sudo --preserve-env=INFINITY_PUBLIC_DOMAIN docker compose config -q
sudo --preserve-env=INFINITY_PUBLIC_DOMAIN docker compose up -d
```

These are **documentation**, not commands already executed. The public
Traefik uses its existing Docker provider; it does not require a new
`providers.file` setting, a restart, or changes to `acme.json`.

Before deployment confirm the domain is unused in any other existing
router, and confirm trusted TLS certificate resolver and firewall behavior.
Do not replace the production `traefik` container.

## Rollback (bridge only)

```bash
cd /opt/portico-infinity-edge
sudo --preserve-env=INFINITY_PUBLIC_DOMAIN docker compose down
```

This removes only the dedicated bridge project. It does NOT modify or
restart the global Traefik and does NOT remove the WireGuard interface.

## Security limitations

- The bridge uses `traefik-public` for outer connectivity but its upstreams
  are exclusively WireGuard IPs. Gateway authentication remains mandatory.
- The sidecar's static HTTP header middleware preserves public HTTPS issuer
  semantics across the extra HTTP proxy hop. Validate real OIDC/PKCE flow and
  callback before release.
- The production public Traefik still has its existing Docker socket, and
  containers sharing `traefik-public` can generally address the bridge.
  The bridge never mounts Docker socket and must not bypass the Gateway OAuth.
- ZITADEL API uses h2c by design. **Plain HTTP to that upstream may break
  ZITADEL gRPC/bootstrap operations.**
- The bridge can be started only after the remote backend network exposure,
  tests and approval. Nothing here installs, starts or changes the VPS.
