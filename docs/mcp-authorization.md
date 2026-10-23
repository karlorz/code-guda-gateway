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
not receive a refresh token. Only operator-password consent may grant
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
