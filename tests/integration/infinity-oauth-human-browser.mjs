// OAuth human/browser proof, exclusively for a disposable GitHub-hosted ZITADEL.
// No bearer, refresh token, password, cookie, authorization code or PAT is logged.
import { createRequire } from 'node:module';
import crypto from 'node:crypto';

const require = createRequire(import.meta.url);
const { chromium } = require('/app/node_modules/playwright');
const issuer = 'https://mcp.example.invalid';
const callback = 'https://ci-callback.example.invalid/callback';
const resourceProject = process.env.PORTICO_RESOURCE_PROJECT;
const password = process.env.CI_OPERATOR_SECRET;
const ownerPAT = process.env.CI_OWNER_PAT;
if (!resourceProject || !password || !ownerPAT) throw Error('missing disposable fixture');
const subject = 'ci-operator-001';
const scope = 'openid profile offline_access urn:zitadel:iam:org:project:id:' +
  resourceProject + ':aud';
const verifier = crypto.randomBytes(32).toString('base64url');
const challenge = crypto.createHash('sha256').update(verifier).digest('base64url');
const state = crypto.randomBytes(24).toString('base64url');
const evidence = { browser_human_login: false, code_pkce: false, refresh_grant: false,
  human_access_introspection: false, mcp_human_before_revocation: false,
  external_human_deactivation: false, revoked_human_access_inactive: false,
  mcp_human_after_revocation_401: false, revoked_refresh_rejected: false,
  short_access_lifetime: false, expired_bearer_inactive: false,
  expired_bearer_mcp_401: false, refresh_after_access_expiry: false,
  human_browser_session_terminated: false, client_chatgpt_real: false };

async function requestJSON(path, options = {}) {
  const response = await fetch(issuer + path, {
    ...options, redirect: 'manual', signal: AbortSignal.timeout(15000)
  });
  const body = await response.json().catch(() => ({}));
  return { status: response.status, body };
}
async function form(path, values) {
  return requestJSON(path, {
    method: 'POST',
    headers: { 'content-type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams(values)
  });
}
async function introspect(token, id, secret) {
  const response = await requestJSON('/oauth/v2/introspect', {
    method: 'POST',
    headers: {
      authorization: 'Basic ' + Buffer.from(id + ':' + secret).toString('base64'),
      'content-type': 'application/x-www-form-urlencoded'
    },
    body: new URLSearchParams({ token })
  });
  if (response.status !== 200) throw Error('introspection HTTP ' + response.status);
  return response.body;
}
async function mcpCode(token) {
  const response = await fetch('http://portico-ci-e2e-gateway:8080/mcp', {
    method: 'POST',
    headers: {
      authorization: 'Bearer ' + token,
      'content-type': 'application/json',
      accept: 'application/json, text/event-stream',
      'MCP-Protocol-Version': '2026-07-28',
      'Mcp-Method': 'tools/list'
    },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/list',
      params: { _meta: {
        'io.modelcontextprotocol/protocolVersion': '2026-07-28',
        'io.modelcontextprotocol/clientInfo': { name: 'human-oauth-disposable', version: '1' },
        'io.modelcontextprotocol/clientCapabilities': {}
      } } }),
    signal: AbortSignal.timeout(10000)
  });
  await response.arrayBuffer();
  return response.status;
}
let browser;
try {
  // Scope is the isolated ZITADEL instance launched for this one GitHub run.
  // Existing service-account credentials were minted before these settings.
  const shortSettings = await requestJSON('/admin/v1/settings/oidc', {
    method: 'PUT',
    headers: {
      authorization: 'Bearer ' + ownerPAT,
      'content-type': 'application/json'
    },
    body: JSON.stringify({
      accessTokenLifetime: '45s',
      idTokenLifetime: '180s',
      refreshTokenIdleExpiration: '120s',
      refreshTokenExpiration: '300s'
    })
  });
  if (![200, 201, 204].includes(shortSettings.status)) {
    throw Error('disposable short-lived token configuration HTTP ' + shortSettings.status);
  }
  const readSettings = await requestJSON('/admin/v1/settings/oidc', {
    headers: { authorization: 'Bearer ' + ownerPAT }
  });
  if (readSettings.status !== 200) {
    throw Error('disposable token-settings read-back HTTP ' + readSettings.status);
  }

  const registration = await requestJSON('/oauth/v2/register', {
    method: 'POST', headers: { 'content-type': 'application/json' },
    body: JSON.stringify({
      client_name: 'Portico disposable human PKCE',
      redirect_uris: [callback],
      grant_types: ['authorization_code', 'refresh_token'],
      response_types: ['code'],
      token_endpoint_auth_method: 'none'
    })
  });
  if (![200, 201].includes(registration.status) || !registration.body.client_id) {
    throw Error('disposable dynamic client registration failed HTTP ' + registration.status);
  }
  const clientId = registration.body.client_id;
  const authorize = new URL(issuer + '/oauth/v2/authorize');
  for (const [key, value] of Object.entries({
    response_type: 'code', client_id: clientId,
    redirect_uri: callback, scope,
    state, code_challenge: challenge, code_challenge_method: 'S256',
    login_hint: 'ci-operator'
  })) authorize.searchParams.set(key, value);

  // The existing CI TLS test separately verifies the private CA and host name.
  // Browser ignores errors only for the private isolated CA in this runner.
  browser = await chromium.launch({ headless: true, args: ['--no-sandbox'] });
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  let callbackURL;
  await page.route('https://ci-callback.example.invalid/**', async route => {
    callbackURL = route.request().url();
    await route.fulfill({ status: 200, contentType: 'text/plain', body: 'OAuth callback received' });
  });

  await page.goto(authorize.toString(), { waitUntil: 'domcontentloaded', timeout: 60000 });
  const username = page.locator(
    'input[autocomplete="username"], input[name="loginName"], input[name="loginname"], input[type="email"], input[type="text"]'
  ).first();
  if (await username.isVisible({ timeout: 15000 }).catch(() => false)) {
    await username.fill('ci-operator');
    await page.locator('button[type="submit"]').first().click();
  }
  const passwordInput = page.locator('input[type="password"]').first();
  await passwordInput.waitFor({ state: 'visible', timeout: 30000 });
  await passwordInput.fill(password);
  await page.locator('button[type="submit"]').first().click();
  await page.waitForURL(url => url.origin === 'https://ci-callback.example.invalid',
    { timeout: 45000 });
  const returned = new URL(callbackURL || page.url());
  if (returned.searchParams.get('state') !== state || !returned.searchParams.get('code')) {
    throw Error('OIDC browser did not return valid code/state');
  }
  evidence.browser_human_login = true;

  const token = await form('/oauth/v2/token', {
    grant_type: 'authorization_code', client_id: clientId,
    code: returned.searchParams.get('code'),
    code_verifier: verifier, redirect_uri: callback
  });
  if (token.status !== 200 || !token.body.access_token || !token.body.refresh_token) {
    throw Error('human PKCE code exchange failed HTTP ' + token.status);
  }
  evidence.code_pkce = true;

  const introspector = {clientId:process.env.CI_INTROSPECTION_CLIENT_ID,clientSecret:process.env.CI_INTROSPECTION_CLIENT_SECRET};
  const introspect1 = await introspect(token.body.access_token,
    introspector.clientId, introspector.clientSecret);
  const audience = typeof introspect1.aud === 'string'
    ? [introspect1.aud] : (introspect1.aud || []);
  if (introspect1.active !== true || introspect1.sub !== subject ||
      !audience.includes(resourceProject)) throw Error('human subject/audience validation failed');
  const firstLifetime = Number(introspect1.exp) - Math.floor(Date.now() / 1000);
  if (!Number.isFinite(firstLifetime) || firstLifetime < 8 || firstLifetime > 65) {
    throw Error('human access token did not receive disposable short lifetime');
  }
  evidence.short_access_lifetime = true;
  evidence.human_access_introspection = true;
  const firstMCPStatus = await mcpCode(token.body.access_token);
  if (firstMCPStatus !== 200) {
    throw Error('valid human token did not access MCP tools/list: HTTP ' + firstMCPStatus);
  }
  evidence.mcp_human_before_revocation = true;

  const refreshed = await form('/oauth/v2/token', {
    grant_type: 'refresh_token', client_id: clientId,
    refresh_token: token.body.refresh_token
  });
  if (refreshed.status !== 200 || !refreshed.body.access_token ||
      !refreshed.body.refresh_token) {
    throw Error('human refresh grant failed HTTP ' + refreshed.status);
  }
  const introspect2 = await introspect(refreshed.body.access_token,
    introspector.clientId, introspector.clientSecret);
  const refreshedMCPStatus = await mcpCode(refreshed.body.access_token);
  if (introspect2.active !== true || introspect2.sub !== subject ||
      refreshedMCPStatus !== 200) {
    throw Error('refreshed human bearer rejected HTTP ' + refreshedMCPStatus);
  }
  evidence.refresh_grant = true;

  // Real wall-clock expiration, not a forged timestamp or forced revocation.
  // Poll against ZITADEL and Gateway independently and bound the wait.
  const expiryDeadline = Date.now() + 75000;
  const firstExpiry = Number(introspect1.exp) * 1000;
  if (!Number.isFinite(firstExpiry)) throw Error('missing token exp from real ZITADEL');
  if (firstExpiry > expiryDeadline) throw Error('access expiry exceeds disposable test budget');
  while (Date.now() < firstExpiry + 1800) {
    if (Date.now() > expiryDeadline) throw Error('real access expiry timed out');
    await new Promise(resolve => setTimeout(resolve, Math.min(1000, firstExpiry + 1800 - Date.now())));
  }
  const expired = await introspect(token.body.access_token,
    introspector.clientId, introspector.clientSecret);
  if (expired.active !== false) throw Error('expired human bearer still introspects as active');
  evidence.expired_bearer_inactive = true;
  if (await mcpCode(token.body.access_token) !== 401) {
    throw Error('real Gateway accepted expired human access bearer');
  }
  evidence.expired_bearer_mcp_401 = true;

  // Refresh must remain useful after ACCESS expiry, until its own policy expires.
  const afterExpiry = await form('/oauth/v2/token', {
    grant_type: 'refresh_token', client_id: clientId,
    refresh_token: refreshed.body.refresh_token
  });
  if (afterExpiry.status !== 200 || !afterExpiry.body.access_token ||
      !afterExpiry.body.refresh_token ||
      await mcpCode(afterExpiry.body.access_token) !== 200) {
    throw Error('human refresh failed after access expiry HTTP ' + afterExpiry.status);
  }
  evidence.refresh_after_access_expiry = true;

  // Explicit *server-side browser SSO session termination*, not token
  // revocation. Logout does not by itself imply that previously minted
  // bearer/refresh tokens have been revoked (those are checked separately).
  const searchSessions = async () => requestJSON('/v2/sessions/search', {
    method: 'POST',
    headers: { authorization: 'Bearer ' + ownerPAT,
      'content-type': 'application/json' },
    body: JSON.stringify({ queries: [{ userIdQuery: { id: subject } }] })
  });
  const beforeSessions = await searchSessions();
  if (beforeSessions.status !== 200 || !Array.isArray(beforeSessions.body.sessions) ||
      beforeSessions.body.sessions.length === 0) {
    throw Error('disposable human Login V2 session missing from session search');
  }
  const sessionIds = beforeSessions.body.sessions.map(session => session.id);
  if (sessionIds.some(id => typeof id !== 'string' || id.length === 0)) {
    throw Error('session search returned invalid session identifier');
  }
  for (const id of sessionIds) {
    const terminated = await requestJSON('/v2/sessions/' + encodeURIComponent(id), {
      method: 'DELETE', headers: { authorization: 'Bearer ' + ownerPAT }
    });
    if (![200, 204].includes(terminated.status)) {
      throw Error('server-side logout rejected HTTP ' + terminated.status);
    }
  }
  let deleted = false;
  for (let attempt = 0; attempt < 20; attempt++) {
    const afterSessions = await searchSessions();
    if (afterSessions.status !== 200 || !Array.isArray(afterSessions.body.sessions)) {
      throw Error('session search failed after server-side logout');
    }
    if (afterSessions.body.sessions.every(item => !sessionIds.includes(item.id))) {
      deleted = true;
      break;
    }
    await new Promise(resolve => setTimeout(resolve, 400));
  }
  if (!deleted) throw Error('human browser SSO session still exists after termination');
  evidence.human_browser_session_terminated = true;

  const deactivated = await requestJSON('/v2/users/' + subject + '/deactivate', {
    method: 'POST', headers: { authorization: 'Bearer ' + ownerPAT }
  });
  if (![200, 204].includes(deactivated.status)) {
    throw Error('external human deactivation HTTP ' + deactivated.status);
  }
  evidence.external_human_deactivation = true;

  // Bound retry for eventual consistency, never succeed on status alone.
  let inactive = false;
  for (let i = 0; i < 16; i++) {
    const after = await introspect(afterExpiry.body.access_token,
      introspector.clientId, introspector.clientSecret);
    if (after.active === false) { inactive = true; break; }
    await new Promise(resolve => setTimeout(resolve, 500));
  }
  if (!inactive) throw Error('deactivated human access token remains active');
  evidence.revoked_human_access_inactive = true;
  if (await mcpCode(afterExpiry.body.access_token) !== 401) {
    throw Error('MCP still accepts revoked human token');
  }
  evidence.mcp_human_after_revocation_401 = true;
  const retryRefresh = await form('/oauth/v2/token', {
    grant_type: 'refresh_token', client_id: clientId,
    refresh_token: afterExpiry.body.refresh_token
  });
  if (retryRefresh.status === 200 || retryRefresh.body.access_token) {
    throw Error('deactivated human refresh still returns token');
  }
  if (![400, 401].includes(retryRefresh.status)) {
    throw Error('unexpected revoked refresh status ' + retryRefresh.status);
  }
  evidence.revoked_refresh_rejected = true;
  console.log('PORTICO_HUMAN_EVIDENCE_JSON=' + JSON.stringify(evidence));
  console.log('INFINITY_HUMAN_BROWSER_PKCE_REFRESH_REVOKE=PASS');
} catch (error) {
  // Avoid Playwright error messages containing authorization URLs and secrets.
  console.error('INFINITY_HUMAN_BROWSER_PKCE_REFRESH_REVOKE=FAIL');
  console.error(String(error?.message || '').replaceAll(/https?:\/\/\S+/g, '[redacted-url]')
    .slice(0, 200));
  process.exitCode = 1;
} finally {
  await browser?.close();
}
