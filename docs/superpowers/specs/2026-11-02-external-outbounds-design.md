# External tunnel final outbounds

Approved in chat on 2026-11-02. Base provenance is recorded in [NOTICE.md](../../../NOTICE.md). Source imported from the official
tag archive because Git smart-HTTP stalled. No LucX source is reused.

## Intent and scope

Clients connect to existing panel inbounds. Selected traffic exits through an
existing remote NaiveProxy, mieru, or TrustTunnel server. The panel manages the
corresponding official local client. No remote service deployment, multi-hop
chain builder, new inbound protocol, or LucX-wide migration is included.

## Approach

Use managed client processes with loopback SOCKS5 interfaces and inject ordinary
Xray SOCKS outbounds under unique tags. This preserves the Xray core and existing
routing. Manually managed SOCKS clients are suitable for a spike but do not meet
the integrated management goal; replacing Xray or adopting all of LucX exceeds
the agreed scope. Implement integrations independently from official client docs.

## Product behavior

- Linux-local final exits for NaiveProxy, mieru and TrustTunnel. Initial binaries:
  Linux amd64 and arm64 where official release artifacts exist.
- Manual server/port/credentials/protocol settings, unique immutable tag, remark,
  enable/disable, status, bounded sanitized logs and HTTPS connectivity probe.
- Existing routing selects tags; no automatic global traffic takeover or node
  configuration distribution. Referenced exits cannot be deleted.
- Enabled processes restore on panel boot and retry with bounded backoff after
  exit. Disable/delete closes existing managed sessions and stops its process.
- Failed/disabled exits block assigned traffic; never substitute direct.
- Invalid saves preserve the previous configuration. Secrets are write-only in
  the API, configuration files private, certificate verification enabled.
- A manager-owned loopback gate isolates Xray from port reuse and stops forwarding
  when the owned client is unavailable. TCP CONNECT only in the first gate;
  unsupported UDP is explicitly rejected and disclosed, never bypassed.
- Existing Xray DNS/routing behavior remains; host-wide DNS tunneling is not
  promised. DNS over TCP/HTTPS can follow the configured routing.

## Implementation choices

Keep external exits in a dedicated GORM model registered in both migration lists.
Use existing authenticated/CSRF-protected API middleware and React/Ant Design
patterns. Add the feature within Xray's outbounds area and include its tags in
existing selectors. Save APIs validate against manual/subscription tags and
protect references. Official client downloads are explicit, version/checksum
pinned, and separate from saving settings. No third-party Go/npm dependency is
needed. Version/architecture manifests and integration details are implementation
decisions checked against official docs and recorded in the user guide.

## Verification and boundaries

Test rendered client configs, validation, secret redaction, process restart/stop,
gate failure isolation, durable storage and route-reference protection. Run
existing API registry, generation, frontend checks, Go tests and builds. Real
HTTPS traffic must be distinguished from process/config/mock tests. Where remote
protocol fixtures or Linux privileges are unavailable, explicitly record what
did not run. No production server, push, publish or deployment is authorized.
