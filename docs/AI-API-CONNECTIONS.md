# AI Agents & API Connections

JustVoxel provides standard MCP and REST interfaces for AI agents and automation.
Sign in as the primary `voxel` administrator and open **Control Center → AI & API
Connections**. The gateway starts disabled. Configure a connection and enable the
gateway before using it. You can configure multiple independent connections;
there is no fixed connection-count limit. Names such as an AI product, a home
automation service, or a script are labels and grant no special permissions.

JustVoxel never needs an AI provider's model API key merely to expose MCP or REST.

## Architecture and security boundary

```text
External MCP/REST
  -> unprivileged WebUI Gateway (existing WebUI listener)
  -> existing UID-restricted Unix-socket Management API
  -> privileged Management Agent
  -> existing appliance operations -> system
```

There is no additional daemon, listener, container, database engine, or management
engine. The privileged Agent is never exposed directly to the network. The Agent
owns connection records, authorization, token digests, OAuth bindings, path
selectors, audit records and allowance accounting in the existing private SQLite
identity/state database. Schema migration 5 extends that database without
replacing browser accounts or sessions.

One explicit capability catalog in the Agent defines REST routes, MCP tools,
minimum levels and instance requirements. Both transports dispatch those catalog
operations, then the existing authoritative handlers perform their normal
validation, locking, player checks, backup checks, review/apply and recovery.
There is no arbitrary Management API proxy, command execution, or raw shell API.

Every request resolves the credential against current registry state. A browser
cookie does not authenticate MCP/REST, and an external bearer credential does not
authenticate the browser workspace. Browser changes retain the existing CSRF
protection. Viewer, Operator, Administrator and primary `voxel` browser roles
retain their existing meanings and never implicitly become Level 4.

## Endpoints and discovery

Use your appliance WebUI origin (scheme, host and port) with these paths:

| Interface | Path | Authentication |
| --- | --- | --- |
| MCP Streamable HTTP | `/mcp` | Connection bearer credential |
| REST | `/api/v1` | Connection bearer credential |
| OpenAPI 3.1.2 | `/api/v1/openapi.json` | Connection bearer credential |
| RFC 9728 metadata | `/.well-known/oauth-protected-resource/mcp` | Public discovery |
| Root metadata alias | `/.well-known/oauth-protected-resource` | Public discovery |

OpenAPI is filtered to the connection's current capabilities. Each operation has
`x-justvoxel-min-level` and `x-justvoxel-all-instances` annotations. It is generated
from the same authoritative catalog that controls execution and MCP visibility.
Refresh discovery after changing permission. Requests have bounded body/result
sizes (1 MiB); individual authoritative operations may have smaller input limits.
Log reads have the existing 200-line maximum and strings are bounded/redacted.

MCP uses the official Go SDK v1.8.0, stateless Streamable HTTP and its supported
protocol negotiation, including 2026-07-28. The current stateless endpoint uses
HTTP POST and does not expose the legacy standalone GET/SSE session stream.
No persistent external MCP sessions are stored. Tools appear according to the
authenticated connection's current level and scope. Each invocation authenticates
and authorizes again in the Agent;
a cached tool name cannot preserve a higher permission. JustVoxel does not add
prompts, resources, model sampling, or model-provider credentials.

A generic MCP client needs the `/mcp` URL, Streamable HTTP support and a supported
way to send its bearer credential. OAuth clients additionally need compatibility
with the configured external authorization server and resource metadata flow.
There is no JustVoxel client registration or authorization/token endpoint.

## Create and use a scoped API token

1. Select **Add Connection**, choose a name and optional type/purpose.
2. Select **Scoped API Token**, the least access level needed, enabled state,
   optional expiration and all or selected Minecraft instances.
3. Select **Apply**. For Level 4, complete the special confirmation first.
4. Copy the token from the one-time modal and store it securely in the client.
5. Enable the gateway when ready. Send `Authorization: Bearer …` on each request.

Tokens use the `jvapi_` prefix and 256 random bits. Only a SHA-256 digest and a
safe fingerprint/last characters are persisted. Authentication compares digests
in constant time. Complete tokens are returned only on creation or rotation with
`Cache-Control: no-store`; they are not redisplayed in settings or placed in
browser storage, logs, audits, operation journals or documentation examples.

**Rotate token** invalidates the previous token immediately and displays its
replacement once. **Revoke token** leaves the record but removes its credential;
rotate to issue a new one. **Delete** removes the connection and its credential,
OAuth binding, allowances and opaque selectors. Disabling a connection denies
new requests without replacing its token. Re-enabling restores access only if
its credential remains valid and it has not expired.

## Access levels

Levels are cumulative, but no level grants connection/security administration,
WebUI user/password administration, raw terminal/shell, arbitrary commands or
Full Factory Reset through the gateway.

| Level | Capabilities | Boundaries |
| --- | --- | --- |
| **1 — Read Only** | Safe appliance status; scoped registered inventory; public instance configuration/status; players; sanitized logs; backup inventory/status | No mutation; whitelist access begins at Level 2 |
| **2 — Basic Management** | Level 1; start/restart; whitelist list/add/remove; request manual backup | Existing Operator protections; **no Stop**, configuration, restore, delete, storage, network, system, users or security changes |
| **3 — Full Management** | Levels 1–2; Administrator start/restart/**Stop**; create/configure/rename/activate/recover/delete instances; Minecraft version/container updates and acknowledgements; implemented plugin/add-on workflows; backup configuration/restore planning/apply/status/recovery; supported export/import/recovery; normal storage/provision/mount/data-migration and network workflows | Appliance-wide operations require all-instance scope. No host systemd/power/OS maintenance, user/password or gateway administration |
| **4 — Full System Control (Developer Mode)** | Level 3; supported host update/check and reboot/power-off workflows; explicit typed systemd inspection/control | Highly privileged. No self-permission changes, new connections, credential rotation, AI/API security-policy changes, Full Factory Reset or root shell |

The current Level 2 restart and manual-backup allowances are **two accepted
actions of each kind per connection**, with a **15-minute cooldown** for each
kind. The existing appliance-wide Operator cooldown also applies, so separate
connections cannot evade it. Failed/refused actions do not consume allowance.
These counters are independent of `web_users` IDs and of other connections.
Raising/lowering a level does not reset usage. The primary administrator can use
**Reset restart / backup allowances**; this resets counts while preserving the
established cooldown timestamps. Level 3/4 use Administrator operation policy.

Level 4 systemd accepts only `status`, `start`, `stop`, `restart`, `enable`,
`disable`, `mask`, `unmask` for one strictly validated unit name, or
`daemon-reload` without a unit. `status` returns selected state properties.
The Agent invokes `systemctl` directly with argv and an option terminator,
never a shell. Wildcards, paths, command fragments, unit editing/linking and
arbitrary environment changes are not accepted. Changing services can make
JustVoxel unavailable: grant Developer Mode only to a principal you trust with
that responsibility.

## Instance scope and live permission changes

Choose all instances or a selected allowlist of immutable `jv-*` IDs. Friendly
names are presentation only. Inventory is filtered; direct instance routes and
nested instance selectors are checked by the Agent. Deleted/retired identities
never silently map to replacement instances. Selected scope does not grant
appliance-wide storage/network/host actions or instance creation; those require
all-instance scope as well as the relevant access level.

Select new settings and **Apply** to change Levels 1–3 or downgrade from Level 4.
The same credential immediately uses the new level/scope on its next request.
An already accepted operation may finish under the existing operation model.
An upgrade to Level 4 from **any lower level always requires confirmation again**:
the dialog identifies the connection and warns about privileged control of
services, storage, network/firewall and availability. Slide fully, acknowledge
with the switch, then select **Enable Level 4**. The final button stays disabled
until both are satisfied. Cancel leaves the saved permission unchanged.
Only the authenticated primary human administrator can approve this transition.

## OAuth resource-server configuration

JustVoxel is an OAuth **resource server**, not an authorization server. It does
not turn the `voxel` password into access tokens. Bring an external standards-based
authorization server that can issue signed JWT access tokens for JustVoxel.
Opaque tokens/introspection and automatic unknown-identity enrollment are not
supported by this implementation.

For each OAuth connection, explicitly configure:

- The exact HTTPS issuer identifier (`iss`).
- The expected HTTPS resource/audience (`aud`), normally the protected external
  origin plus `/mcp`, also used for that connection's REST access.
- The issuer's HTTPS JWKS endpoint (from its trusted discovery metadata).
- Required scope `justvoxel:connect`.
- A stable `sub` and/or `client_id` binding. At least one is required; when both
  are configured, both must match.

The Agent verifies the signature against the administrator-pinned JWKS URI,
issuer, audience, required expiration, not-before when present, issued-at when
present, scope and stable binding. Tokens for a different resource are rejected.
Allowed signing algorithms are RS256/RS384/RS512 (RSA keys at least 2048 bits)
and ES256 (P-256). JWT-selected key URLs and HTTP redirects are not followed.
Public signing keys are briefly cached (one minute); access tokens and their
claims are never persisted. Network fetches have a timeout and bounded size.

Multiple issuers and identities can coexist. A token must match **exactly one**
enabled, unexpired configured connection; overlapping bindings fail closed.
Deleting/disabling a connection takes effect even with a still-valid issuer
access token. The public resource metadata advertises enabled configured issuers
for the requested resource, and unauthenticated protected access returns a
Bearer 401 challenge pointing to it. Configure the audience to match the URL
clients actually use. HTTPS is required for OAuth configuration even though the
local WebUI default remains HTTP; protected remote access must be arranged
separately. Never paste an OAuth access token or issuer private key into settings.

## REST requests and results

These examples contain placeholders only. Replace the origin and credential in
your client; avoid shell history containing real tokens.

```sh
curl --header 'Authorization: Bearer <JUSTVOXEL_TOKEN>' \
  'https://justvoxel.example/api/v1/openapi.json'

curl --header 'Authorization: Bearer <JUSTVOXEL_TOKEN>' \
  'https://justvoxel.example/api/v1/instances'

curl --header 'Authorization: Bearer <JUSTVOXEL_TOKEN>' \
  'https://justvoxel.example/api/v1/instances/jv-abcdef/status'

curl --request POST --header 'Authorization: Bearer <JUSTVOXEL_TOKEN>' \
  --header 'Content-Type: application/json' --data '{"confirm_players":false}' \
  'https://justvoxel.example/api/v1/instances/jv-abcdef/restart'

curl --request POST --header 'Authorization: Bearer <JUSTVOXEL_TOKEN>' \
  --header 'Content-Type: application/json' \
  --data '{"platform":"java","action":"add","name":"ExamplePlayer"}' \
  'https://justvoxel.example/api/v1/instances/jv-abcdef/whitelist'
```

REST request bodies are the existing typed appliance-operation requests. Review
and apply remain separate where the existing workflow requires them; retain the
returned plan fingerprint and explicit confirmation data. If a lifecycle response
reports players requiring confirmation, review the players and resubmit with
`confirm_players:true` only when intended. A successful HTTP response can describe
an accepted/in-progress operation or a required confirmation, not completion.
Follow the corresponding status/current-operation route and review recovery in
the human WebUI when necessary. Never replace recovery with Factory Reset.

MCP tools share the same operation names as OpenAPI `operationId`. Arguments use
`instance_id` for selected operations, `parameters` for other path placeholders,
`query` for supported query parameters, and `body` for the existing typed JSON
request. For example `instance_restart` takes an immutable instance ID and
`body:{"confirm_players":false}`. Tools return sanitized JSON as text and report
operation failures as MCP tool errors.

Private paths are represented by connection-bound opaque `jvref_*` selectors in
approved discovery fields. Pass those selectors back unchanged in typed plan/apply
requests. They convey no additional permission; the Agent resolves them and the
existing operation still validates ownership, plans and safety. They cannot be
used across connections. Credentials, hashes, RCON secrets, private runtime
state and Floodgate private material are excluded; diagnostic text is redacted.

## Gateway state, Factory Reset and auditing

Disabling the gateway blocks all new MCP/REST management requests but retains
configured connections. The workspace shows **Enabled/Disabled** and **Last
Activity** (successful authenticated requests), not a fabricated active-session
or Connected state. Refresh to read current activity and settings.

Once reviewed Full Factory Reset commits to execution, the Agent first durably
disables the gateway and deletes **all** connections, token digests, OAuth bindings,
usage and opaque selectors, before destructive cleanup. This covers every level,
authentication mode and enabled state. A later reset failure/Needs Attention does
not restore old credentials, and retries cannot resurrect them. Connection
administration is blocked while reset remains active/needs attention. Complete or
resolve the supported reset recovery first. The final state has zero connections
and a disabled gateway; new connections require explicit human configuration.
Existing browser-session invalidation remains. The reset review explains this
revocation. JustVoxel cannot erase a token cached by an external provider; it
makes that credential unusable on JustVoxel.

Safe audit events identify connection ID/name for creation, enabling/disabling,
level changes (including Level 4 entry/exit), OAuth binding changes, rotation,
revocation, deletion, external mutation attempts/results and Factory Reset
revocation. They never contain bearer credentials, token digests, private claims
or passwords. A successful full reset clears normal history under the established
reset policy; revocation is independently committed before cleanup starts.

## Trusted networks, compatibility and troubleshooting

The default `http://justvoxel.local:8099` and LAN-IP URLs are unencrypted. Bearer
credentials grant access to whoever possesses them: use only a trusted network
locally, or arrange trusted HTTPS/private VPN access for remote use. Do not expose
port **8099 directly to the public Internet**. This feature adds no TLS listener,
reverse proxy, VPN, or firewall opening.

When TLS terminates at a trusted reverse proxy, preserve the external Host and
forward `X-Forwarded-Proto: https` so OAuth protected-resource discovery reports
the HTTPS MCP resource.

Your AI agent interaction and connection with JustVoxel through MCP or REST API
depends on your AI provider, application, package/subscription, and supported
connection methods. JustVoxel provides standard MCP and REST API interfaces, but
connection availability is determined by the AI agent or service provider.
No universal compatibility is promised for ChatGPT, Claude, Gemini, Grok, Copilot,
OpenClaw or another vendor, nor for every subscription from those providers.

| Symptom | Check |
| --- | --- |
| 401 / authentication required | Gateway enabled; connection enabled/unexpired; current token; exact OAuth issuer/audience/scope/binding; reachable pinned JWKS; appliance clock |
| 403 / operation absent | Current level, selected instance scope, all-instance requirement; refresh MCP tool list/OpenAPI after Apply |
| Level 4 Apply refused | Slide and acknowledge in the special primary-administrator dialog each time you enter from a lower level |
| Token unavailable in settings | It is shown once. Rotate and update the client; the old token stops working |
| Restart/backup refused | Operator limits, personal/global cooldown, player safety, active locks or operation state; review WebUI before retrying |
| Plan/apply fails | Use current discovery, opaque selectors from the same connection, exact plan fingerprint and required confirmation; inspect operation/recovery in WebUI |
| OAuth identity ambiguous | Remove overlapping bindings; one validated identity must select exactly one connection |
| Provider cannot connect | Verify its Streamable HTTP/bearer/OAuth/resource support, package/subscription and protected network reachability |
| Connection settings blocked after reset failure | Complete Retry/Recovery or supported keep-current-state resolution; revoked credentials stay invalid |

Source tests are separate from image/VM acceptance. Gateway tests live beside
the Management Agent/WebUI packages and in `tests/agent-gateway.test.cjs`. Passing
source tests does not establish image-build, runtime, or visual acceptance; CI
image build and VM validation remain separate.
