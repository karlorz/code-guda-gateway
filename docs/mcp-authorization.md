# MCP authorization compatibility

The gateway is the authorization server for the canonical MCP resource
`<GUDA_OAUTH_ISSUER>/mcp`. GrokSearch remains a separate resource server and
verifies opaque tokens using the authenticated loopback verification endpoint.
Never forward inbound MCP credentials to provider APIs.

## Client registration and consent

Existing dynamic registration at `/register` remains available. Requests are
limited to 16 KiB, 16 callback URIs, and 60 registrations per minute per process.
Clients may instead use an HTTPS client metadata-document URL as `client_id`,
without registering. Documents must identify their own URL, name the client,
and supply valid callback URIs. Metadata fetches use verified TLS, no ambient
proxy/cookies, a five-second timeout, a 64 KiB limit, and no redirects. DNS
addresses are checked and pinned at dial time; private, loopback, link-local,
mapped-private, and reserved destinations are rejected. Metadata is revalidated
for each authorization step rather than stored as a permanent registration.

Callback URIs match exactly, except that an IP-literal HTTP loopback callback
may vary its port under RFC 8252. The callback path and query must still match.
Userinfo, fragments, and wildcard hosts are rejected. Consent displays the
client identity and requested scopes. PKCE S256 remains mandatory.

## Resource and scope binding

An explicit `resource` must equal the canonical MCP resource on authorization,
code exchange, and refresh. Older clients may omit it because this authorization
server serves one resource; persisted grants still contain an explicit target.
Verification rejects a different or empty stored target. The database contains
opaque service-owned grants, not portable JWTs; no missing-`aud` JWT bypass is
introduced.

Migration 0012 preserves registrations and credentials and adds scope, resource,
and refresh-family state. Startup binds preexisting blank-target OAuth rows to
the configured resource. Already-bound targets are never overwritten, so changing
the configured issuer does not silently retarget those tokens. Existing grants
retain their previously issued `mcp offline_access` permissions.

Supported scopes are `mcp`, `offline_access`, and `config:write`. Omitting scopes
retains the legacy `mcp offline_access` default. A request with only `mcp` does
not receive a refresh token. Only authenticated operator consent (password or an allowed GitHub account) may grant
`config:write`; public/invite issuance cannot obtain it. GrokSearch checks this
permission before `switch_model` and mutating `toggle_builtin_tools` calls.
Existing manually issued host credentials and local stdio keep their operator
permissions. Existing OAuth clients retain search access and need explicit new
consent only if they need settings changes.

## Refresh and compatibility

Access lifetime remains two hours. Refresh families have an absolute fourteen-day
lifetime. Refresh rotates the key and refresh token atomically. A replay of a
consumed, unexpired refresh token disables every successor key in its family.
Clients must serialize refresh and persist each replacement before another use;
a lost refresh response requires authorization again, not retrying the old token.
Only hashed refresh history is stored. Revoked/disabled keys cannot refresh.

## Rollout and verification

Deploy the gateway before GrokSearch. Preserve the stack-owned Caddy configuration
(use `--skip-caddy` on the binary installer), back up the database and current
binaries, then update exact stack pins. Additive database columns permit a binary
rollback; restoring the database is only needed for a failed migration and must
not discard grants created after a successful rollout.

Automated coverage includes legacy DCR/PKCE/pairing, no-registration metadata
clients, wrong-resource rejection, scope retention, no-offline refresh denial,
refresh replay/family invalidation, redirect validation, and real HTTP MCP calls
with read-only, configuration, manual-host and static-host credentials. Run
`go test -race ./...` plus the installer tests. GrokSearch's pytest suite supplies
the resource-server regression coverage.

## GitHub operator sign-in

Settings → **GitHub operator sign-in** optionally adds GitHub alongside the
existing operator password on `/authorize`. It authenticates the operator only;
it does not sign into the admin console, change existing grants, or approve an
MCP client automatically. After GitHub sign-in the operator must review the
original client identity and requested permissions and click **Approve Access**.
An allowed GitHub operator can explicitly approve `config:write`, just as an
operator using the password can. Public/invite grants remain unable to do so.

Create a dedicated GitHub **OAuth App** with homepage set to the gateway origin.
Set its authorization callback URL to the value shown in Settings, for example:

```text
https://search.karldigi.dev/authorize/github/callback
```

While signed into the gateway admin console, enter the app's client ID and
client secret and an explicit list of allowed **numeric GitHub user IDs**, then
enable sign-in. An account's public `id` is available from
`https://api.github.com/users/USERNAME`; usernames and email addresses are not
access-control identities because they may change or be reassigned. No account
is implicitly allowed. A new OAuth App needs no repository or email scopes for
this identity check. Use a dedicated app to avoid previously granted broader
scopes from other integrations. Complete GitHub login and any consent in the
operator's own browser; agents never need the operator password or GitHub token.

The app secret is encrypted in SQLite with the existing master key. Settings
reads expose only `secret_configured`; leaving the secret blank preserves it
for the same client ID. Changing the client ID requires that app's secret.
The authenticated, CSRF-protected settings endpoint is
`GET|PATCH /admin/api/settings/github-operator`. Changing or disabling these
settings invalidates outstanding GitHub login/consent proofs, without revoking
already issued MCP grants. Password login remains available.

GitHub uses an expiring, browser-bound, one-use state and PKCE S256. The fixed
callback is derived from the configured issuer, never an inbound Host header or
user-supplied return URL. Token exchange and identity lookup are fixed-host,
verified-TLS requests with no redirects or ambient proxy, bounded time and
64 KiB response limits. GitHub access tokens are used only for the identity
lookup and are never stored or returned. The resulting one-use consent proof
binds the browser, client, redirect, original MCP state/PKCE, resource, scope,
agent label and invite value. It cannot approve another client or altered
permissions. Outstanding flows expire after ten minutes per phase; only hashed
proofs/browser bindings and encrypted transient payloads are persisted.

Migration 0013 adds only `operator_login_flows`; it does not rewrite existing
credentials. Production rollout must suppress access logging for both
`/authorize` and `/authorize/*` (including the GitHub callback), as well as token
and CLI callback paths. Use the reviewed gateway template or update the
stack-owned Caddy template before enabling GitHub. Keep app secrets, codes,
state/proof values and cookie values out of logs and diagnostic output.

Automated tests mock GitHub's fixed endpoints to exercise success, denied IDs,
state/cookie mismatches, expiry, replay, permission tampering, settings rotation,
concurrent use, response limits and secret redaction. Live GitHub acceptance
requires a configured OAuth App and an operator browser login; passing mocked
tests alone does not prove that external setup is correct.
