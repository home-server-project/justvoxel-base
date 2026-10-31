# AI Agents & API Connections

JustVoxel provides standard MCP and REST interfaces for AI agents and automation.
Sign in as the primary `voxel` administrator and open **Control Center → AI & API
Connections**. The gateway starts disabled. Configure a connection and enable the
gateway before using it. You can configure multiple independent connections;
there is no fixed connection-count limit. Names such as an AI product, a home
automation service, or a script are labels and grant no special permissions.

JustVoxel never needs an AI provider's model API key merely to expose MCP or REST.

## Workspace

The overview starts with a compact gateway Enabled/Disabled switch and configured
and enabled connection counts. Disabling it keeps your connections. Below it,
responsive cards follow the Minecraft Servers layout. Cards show the name,
provider identity, readable access level, Minecraft server scope,
enabled state and Last activity. Select **Settings** on a card to edit it.

The top action lane contains **Add Connection**, then **Connection help**.
**Add Connection** opens a provider-first setup surface. Enter a **Name** and
choose **OpenAI** (ChatGPT & Codex), **Claude**, **Grok**, **Gemini**, **GitHub
Copilot**, **Other AI / MCP app**, or **Automation / REST API**. OpenAI is one card
for both ChatGPT and Codex. The default is OpenAI, Level 1 — Read Only, all
Minecraft servers, and Enabled. Choose the permissions and servers, then select
**Create**. OpenAI, Claude, Grok, Gemini and GitHub Copilot use normal JustVoxel
OAuth login. Settings shows **Ready to connect**, a connection-specific MCP
address and **Sign in with JustVoxel**. Copy the address into the AI app; sign in
normally as the primary `voxel` administrator and select **Allow** after reviewing
the connection permissions. The client receives tokens automatically; users never
copy an access token. Other AI / MCP app and Automation / REST API still issue a
scoped token in the one-time dialog. There is no authentication-method selector.
Provider selection is a label stored in the existing purpose field and does not
change permissions or automatically configure the provider. For new connections,
the branded choice selects local OAuth; existing authentication modes stay unchanged.

**Settings** groups the primary controls, status and actions. It shows credential
status and expiration when configured, and offers token rotation/revocation,
allowance reset and deletion. **Advanced details** contains the connection ID,
safe credential fingerprint, created/updated times and optional expiration.
Existing OAuth connections retain editable technical configuration under
**Advanced details → Developer integration**; token connections do not show
OAuth fields. Tokens are shown only once on creation or rotation, never in Settings.
Back or Cancel returns to the overview.

**Connection help** starts with **Choose your app** and high-level provider
guidance. AI cards use the MCP address; Automation / REST API uses the REST
address and connection token. Both addresses retain Copy actions. Provider,
application, account, subscription and network access determine actual support;
selecting a card does not promise availability. Levels 1–4 and remote-access
guidance follow. Technical OAuth explanation and metadata are in the collapsed
**Developer integration** section at the bottom, outside normal setup.

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
identity/state database. Schema migrations 5 and 6 extend that database without
replacing browser accounts, sessions or existing connection credentials.

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
| MCP Streamable HTTP (branded local OAuth) | `/mcp/<ConnectionID>` | Automatically issued local OAuth bearer |
| MCP Streamable HTTP (legacy) | `/mcp` | Scoped token or existing external OAuth bearer |
| REST | `/api/v1` | Connection bearer credential |
| OpenAPI 3.1.2 | `/api/v1/openapi.json` | Connection bearer credential |
| RFC 9728 metadata | `/.well-known/oauth-protected-resource/mcp` | Public discovery |
| Root metadata alias | `/.well-known/oauth-protected-resource` | Public discovery |
| Connection resource metadata | `/.well-known/oauth-protected-resource/mcp/<ConnectionID>` | Public HTTPS discovery |
| Authorization server metadata | `/.well-known/oauth-authorization-server` | Public HTTPS discovery |
| Public client registration | `/oauth/register` | Public HTTPS registration; no client secret |
| Sign-in and consent | `/oauth/authorize` | Normal browser login; primary `voxel` approval |
| Code exchange / refresh | `/oauth/token` | Public client binding, PKCE and opaque credentials |

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
way to send its bearer credential. Existing external OAuth clients additionally
need compatibility with their configured issuer and resource metadata flow.
Branded local OAuth connections use the JustVoxel registration, authorization
and token endpoints; existing external OAuth continues to use its configured issuer.

## Create and use a scoped API token

1. Select **Add Connection**, enter a **Name** and choose **Other AI / MCP app**
   or **Automation / REST API**.
2. Choose the least access level needed and all or selected Minecraft servers.
3. Select **Create**. For Level 4, complete the existing special confirmation first.
4. Copy the JustVoxel connection token once, store it securely in the client, then
   select **Done**. This applies to Other AI / MCP app and Automation / REST API.
5. Use the MCP address for AI apps or the REST address for automation. Enable the
   gateway when ready. Send `Authorization: Bearer …` on each request.

Optional expiration remains editable in the existing connection's **Advanced
details**. Existing external OAuth records remain manageable through
**Developer integration** without migration.

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

## Branded providers: JustVoxel sign-in

New branded connections use `oauth_local`, separate from existing external
`oauth` and scoped `token` modes. Their exact resource is
`https://<host>/mcp/<ConnectionID>`; the ID is public routing information, not a
credential. Copy that address from Settings into the provider/app, sign in using
the normal JustVoxel login, review the configured access level and Minecraft
scope, then select **Allow**. Only the primary human `voxel` administrator can
approve. No model-provider API key is required, and no JustVoxel access token is
shown or copied by the user.

The OAuth 2.1 flow uses authorization code plus mandatory PKCE S256. Public
clients register exact HTTPS redirects or native-client HTTP loopback redirects
(`localhost`, `127.0.0.1`, `[::1]`); no client secret is issued. Pending requests
expire after ten minutes; authorization codes expire after five minutes and are
atomically consumed once. Codes, one-hour access tokens and thirty-day refresh
tokens are stored only as SHA-256 digests. Refresh rotates its token immediately.
Tokens bind the client, connection and exact resource, while each MCP request
loads the current enabled state, access level and Minecraft instance scope.
Disabling/deleting a connection or disabling the gateway denies the next request.

Discovery and authorization require an external HTTPS presentation. Remote AI
providers require a reachable HTTPS MCP address; cloud providers cannot reach a
private LAN address by default. This feature configures no tunnel, proxy, VPN or
public exposure. Provider/application/account/subscription support still varies.
JustVoxel advertises no local JWKS (access tokens are opaque) or CIMD support.

## Developer integration: OAuth resource-server configuration

Existing `auth_mode=oauth` records remain compatibility/admin functionality:
JustVoxel acts as a resource server for their external issuer and JWKS binding.
These records are not migrated to `oauth_local`. Bring an external standards-based
authorization server issuing signed JWT access tokens for these connections;
external opaque-token introspection remains unsupported.

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
usage and opaque selectors, plus all local OAuth requests, codes, access/refresh
token digests and registered gateway clients, before destructive cleanup. This covers every level,
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
