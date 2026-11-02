# External final outbounds implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Manage NaiveProxy, mieru and TrustTunnel as selectable final exits.

**Architecture:** Official local clients expose private SOCKS5 backends. A
manager-owned authenticated loopback gate accepts TCP CONNECT and forwards only
through the owned running client; generated Xray outbounds target this gate.
GORM stores exits and existing routing selects their tags.

**Tech Stack:** Existing Go/Gin/GORM, golang.org/x/net/proxy, React/TypeScript,
Ant Design and TanStack Query. No new Go/npm dependency.

**Spec:** `docs/superpowers/specs/2026-11-02-external-outbounds-design.md`

## Global Constraints

- First scope: Linux-local NaiveProxy, mieru and TrustTunnel final exits.
- TCP CONNECT only; reject unsupported UDP, no direct fallback.
- Write-only secrets; private files; immutable unique tags; referenced deletion refused.
- Independently implemented from official documentation; do not copy LucX code.
- No production deployment, push or publishing.

## Review Focus

- A foreign listener reuses a stopped client port: never forward to it.
- Disable while traffic is active: terminate sessions, keep the tag blocked.
- Concurrent edits or template saves: preserve tag uniqueness and references.
- Credential-bearing child output: logs and API must not return passwords.
- Client crashes during a probe: fail the probe, never retry directly.

### Task 1: Validated protocol configs and protected TCP gate

**Files:** Create `internal/externalproxy/config.go`, `gate.go`, `config_test.go`, `gate_test.go`.

**Interfaces:** `Spec.Validate() error`; `Spec.Render(port int) ([]byte, error)`;
`NewManager(stateDir, binaryDir string) *Manager` in Task 2.

- [ ] Write protocol config/invalid input tests and gate tests for CONNECT,
      authentication, UDP refusal and shutdown, with independently specified fixtures.
- [ ] Run `go test ./internal/externalproxy` and observe missing-feature failures.
- [ ] Implement typed specs and the authenticated manager-owned gate, without
      opening any public listener or using a direct target dial.
- [ ] Run the package tests and race check.

### Task 2: Client lifecycle and pinned installation

**Files:** Create `internal/externalproxy/manager.go`, `listener_linux.go`,
`listener_other.go`, `manager_test.go`, and `scripts/install-external-clients.sh`.

**Interfaces:** `Manager.Reconcile([]Spec) error`, `Manager.Outbounds() []map[string]any`,
`Manager.Status(tag string) Status`, `Manager.StopAll()`.

- [ ] Add failing subprocess tests for retry, stop/disable, active-session closure,
      missing executable, bounded/redacted logs and foreign backend listener refusal.
- [ ] Implement private configs, direct child execution, bounded exponential
      retry and process ownership validation for Linux backend sockets.
- [ ] Pin official binary URLs/checksums for amd64/arm64; install explicitly and
      atomically with no system routing/firewall changes.
- [ ] Run package/race tests and check official client config startup.

### Task 3: Durable service, authenticated API and Xray wiring

**Files:** Create `internal/database/model/external_outbound.go`,
`internal/web/service/external_outbound.go`, `internal/web/controller/external_outbound.go`
and adjacent tests. Modify both migration lists, `service/xray.go`,
`service/xray_setting.go`, `controller/api.go`, `controller/xray_setting.go`,
`web.go`, `tools/openapigen/main.go`, API registry and generated references.

**Interfaces:** `ExternalOutboundService.Save(request)`, `List()`, `Delete(id)`,
`Reconcile()`, `Probe(ctx,id)`. Requests separate from secret-free response views.

- [ ] Write failing real-SQLite/API tests for saved config preservation,
      password retention without exposure, tag collisions, reference deletion,
      disabled blackhole injection and restricted-token rejection.
- [ ] Register the model in both migration lists and implement validation/CRUD.
- [ ] Wire process restore/stop and runtime injection before routing validation;
      surface managed tags through existing Xray config response selectors.
- [ ] Document every new route; generate schemas/OpenAPI/docs.
- [ ] Run service/controller/database/API contract checks.

### Task 4: Existing outbounds UI integration

**Files:** Create `frontend/src/schemas/external-outbound.ts`,
`api/external-outbounds.ts`, `pages/xray/outbounds/ExternalOutbounds.tsx`,
`test/external-outbounds.test.tsx`; modify `XrayPage.tsx` and all locale bundles.

**Interfaces:** Secret-free list schema; write request with optional replacement
password; saved mutations invalidate Xray tag queries.

- [ ] Write failing UI tests for adding/editing, secret-preserving updates,
      disabled referenced exits, failed save feedback and bounded logs.
- [ ] Implement the existing AntD/RHF patterns and a locally scoped query.
- [ ] Add English and Chinese labels plus matching locale keys elsewhere.
- [ ] Run frontend tests, typecheck, lint/format and production build.

### Task 5: Integration verification and review

**Files:** Create `docs/external-outbounds.md` with installation, protocol sources,
TCP/DNS limitations, route example, validation record and rollback instructions.

- [ ] Run full applicable Go/frontend tests, generation checks, builds and
      `git diff --check`; distinguish environment skips from passes.
- [ ] Exercise the actual page, add/edit/disable an exit and inspect logs/console.
- [ ] Run available real protocol fixtures; record unavailable integrations.
- [ ] Request one independent whole-change review; fix substantive findings with
      regression tests; deliver source and build artifacts with verification limits.
