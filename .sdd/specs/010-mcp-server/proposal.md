# Design Proposal: MCP Server for VM Operator

- **Author**: Arunesh Pandey
- **Created**: 2026-09-30
- **Status**: Superseded — reviewed in [`review.md`](./review.md); decisions folded into [`spec.md`](./spec.md), [`plan.md`](./plan.md), [`model.md`](./model.md), and [`research.md`](./research.md). Kept as the design record; where it disagrees with those files, they win.
- **Epic**: TBD
- **Target**: `vmware-tanzu/vm-operator`

> This proposal is the pre-spec design document. After architecture review it is decomposed into `spec.md`, `plan.md`, `tasks.md`, `model.md`, and `research.md` in this directory.

---

## 1. Summary

Add a **Model Context Protocol (MCP) server** for VM Operator: a small, standalone binary that exposes VM Service capabilities (VirtualMachines, classes, images, services, snapshots, publish requests, groups, replica sets) as typed MCP **tools**, **resources**, and **prompts**. Any MCP-capable AI host (Claude Code, Claude Desktop, VS Code / Copilot, Cursor, etc.) can then inspect, troubleshoot, and — when explicitly allowed — operate VMs on a vSphere Supervisor in natural language. The AI acts **as the user**, with the user's own Kubernetes identity and RBAC.

The v1 recommendation is a **client-side, stdio-transport binary** (`vmop-mcp`) that talks only to the Supervisor Kubernetes API using the kubeconfig produced by the existing vSphere / VCF CLI login flow. It is **read-only by default**. Write and destructive tools are opt-in and gated separately. An in-cluster, multi-tenant Streamable HTTP deployment is deferred to a later phase because it needs a new authentication design (Section 7.2).

---

## 2. Background

### 2.1 What MCP is

MCP is an open JSON-RPC protocol that connects AI applications ("hosts") to external systems through "servers". A server advertises:

- **Tools**: typed functions the model may call, each with a JSON-Schema input, an optional output schema, and behavior hints (`readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`).
- **Resources**: addressable, read-only context such as documents or schemas, identified by URI.
- **Prompts**: parameterized, user-invoked workflow templates.

Transports are **stdio**, where the host launches the server as a subprocess, and **Streamable HTTP**, for remote servers, with OAuth 2.1-based authorization. The official Go SDK is `github.com/modelcontextprotocol/go-sdk`. It is stable at v1.x (v1.8.0 at the time of writing) and requires Go ≥ 1.25. This repository is on Go 1.26.8.

### 2.2 Why VM Operator specifically

VM Operator's API is rich. `VirtualMachineSpec` alone has around 35 top-level fields in v1alpha6. Its status is spread across ten or more condition types (`VirtualMachineClassReady`, `VirtualMachineImageReady`, `VirtualMachineStorageReady`, `VirtualMachineBootstrapReady`, `VirtualMachineNetworkReady`, `VirtualMachineConditionPlacementReady`, `VirtualMachineCreated`, …) and cross-object references: class, image or cluster image, storage class, bootstrap Secrets, PVCs, networks, groups, and resource policies. A typical DevOps user who asks "why is my VM not running?" has to know which conditions, events, and referenced objects to inspect. An LLM with generic `kubectl` access can do this poorly and expensively: it spends tokens on `managedFields` and guesses field names. A **domain-specific** MCP server encodes VM Operator's own knowledge of how these objects relate.

### 2.3 Prior art

- **Generic Kubernetes MCP servers**, such as the community `kubernetes-mcp-server` projects. They can read VM Operator CRDs as unstructured objects, but they have no knowledge of VM semantics, return raw YAML, and cannot aggregate diagnostics.
- **In-repo analog**: `cmd/web-console-validator` is a small, separate binary in this repo. It uses a controller-runtime client with the `vmopv1` scheme and no manager. The MCP server follows the same "thin binary + package with logic" shape.

---

## 3. Benefits

| Benefit | Who gains | Why it matters |
|---|---|---|
| Faster VM troubleshooting | DevOps user, support | One `diagnose_virtual_machine` call replaces a series of `kubectl get/describe`, events, class, image, and PVC lookups, and returns a ranked list of findings with next steps. |
| Lower barrier to VM Service | New DevOps users | Discover classes, images, and storage classes, then create a valid VM through a guided flow. Server-side dry-run surfaces webhook validation errors before anything is persisted. |
| Correct-by-construction API use | Everyone | Tool inputs are typed against v1alpha6, so the model cannot invent fields. The live OpenAPI schema is available as a resource. |
| Token-efficient, safe output | AI host / user | Curated summaries: no `managedFields`, no last-applied annotations, and never Secret contents. |
| Zero new cluster attack surface (v1) | CSP admin, security | Runs on the user's workstation with the user's RBAC. Nothing is deployed to the Supervisor. |
| Platform for partner integrations | Partner engineer (VKS, VCFA, support tooling) | A stable, versioned tool contract that other agents can build on. |
| Supportability | Support / platform engineer | A standard "collect VM context" tool yields reproducible, redacted diagnostic bundles to attach to tickets. |

---

## 4. Use cases

### UC-1 Diagnose a VM that is not ready (primary)

"Why is `web-01` in namespace `dev` stuck?" The model calls `diagnose_virtual_machine`. The server gathers:

- the VM's non-True conditions,
- recent Warning events for the VM,
- whether the referenced `VirtualMachineClass` exists and is Ready (and whether it is bound to the namespace),
- whether the image (`VirtualMachineImage` / `ClusterVirtualMachineImage`) exists and is Ready,
- whether the storage class is available,
- the PVC phase for each `spec.volumes[*].persistentVolumeClaim`,
- whether each referenced bootstrap Secret *exists* (never its contents),
- network readiness.

The server returns structured findings (`severity`, `object`, `reason`, `message`, `suggestion`).

### UC-2 Inventory and status questions

- "List my powered-on VMs and their IPs."
- "Which VMs use class `best-effort-large`?"
- "What images are available for Ubuntu?"

These map to `list_*` tools with label selectors and compact summaries.

### UC-3 Guided VM creation

"Create an Ubuntu 24.04 VM with 4 vCPUs and attach it to my default network."

1. The model lists classes, images, and storage classes.
2. It proposes a spec.
3. It calls `create_virtual_machine` with `dryRun=true` and shows the result (including admission webhook errors) to the user.
4. On confirmation, it repeats the call with `dryRun=false`.

### UC-4 Day-2 lifecycle

These operations require opt-in:

- power on, off, or suspend (`spec.powerState`, `spec.powerOffMode`)
- restart (`spec.nextRestartTime: "now"`)
- resize (`spec.className`)
- create a snapshot
- scale a `VirtualMachineReplicaSet`
- publish a VM to a content library (`VirtualMachinePublishRequest`)

### UC-5 Destructive operations

These require a second opt-in:

- delete a VM
- delete a snapshot
- revert to a snapshot (`spec.currentSnapshotName`)

### UC-6 Expose a VM on the network

"Expose port 443 of `web-01` through a load balancer." The model creates a `VirtualMachineService` with a selector that matches the VM's labels. It dry-runs first.

### UC-7 Explain the API

"What does `spec.powerOffMode: TrySoft` do?" The model reads the resource `vmop://openapi/v1alpha6/VirtualMachine`, which the server fetches from the cluster's `/openapi/v3` endpoint so it always matches the server's version.

### UC-8 Support bundle for a VM

"Collect everything support needs for `web-01`." The server returns a redacted, single-document summary of the VM, its referenced objects, conditions, and events.

---

## 5. Scenarios (end-to-end walk-throughs)

### S-1: DevOps user, VM stuck on image

1. The user runs `kubectl vsphere login …` (or the VCF CLI equivalent). The kubeconfig now has context `dev`.
2. The host launches `vmop-mcp --context dev` over stdio.
3. The user asks: "Why isn't `web-01` up?"
4. The model calls `diagnose_virtual_machine{namespace:"dev", name:"web-01"}`.
5. The server reports: `VirtualMachineImageReady=False`, reason `NotFound`; image `vmi-0a1b…` does not exist in `dev`; a Warning event says the image was not found. It suggests `list_virtual_machine_images`.
6. The model lists images, finds the correct name, and proposes changing `spec.imageName`. `spec.image` is immutable after creation in most flows, so the model instead proposes deleting and recreating the VM, and explains why it cannot patch in place.

### S-2: Read-only default blocks mutation

With write tools not enabled, the user asks the model to power off a VM. Write tools are not advertised in `tools/list`, so the model cannot call one. It tells the user that the server is in read-only mode and how to enable writes (`--enable-write`).

### S-3: RBAC denial is reported, not bypassed

The user asks for VMs in namespace `prod` but has no access to it. The API returns 403. The tool returns an `isError` result with the Kubernetes status message. The server has no credentials of its own to fall back on.

### S-4: Token expiry

The vSphere login token expires, typically after 10 hours. The next call returns 401. The server re-reads the kubeconfig on each request (or on a 401), so after `kubectl vsphere login` it recovers without a restart. The tool error tells the user to log in again.

### S-5: Create with dry-run

1. `create_virtual_machine{dryRun:true, …}` → the admission webhook rejects the request because the class is not bound to the namespace.
2. The model surfaces the webhook message, lists bound classes, and retries the dry-run.
3. The user confirms, and the VM is created.

---

## 6. Goals / non-goals (preview for `spec.md`)

**Goals (v1)**
- A read-only tool set covering VM, VM class, VM image, cluster VM image, VM service, VM snapshot, VM publish request, VM group, VM replica set, and events.
- A VM diagnosis aggregator (UC-1) and a support-bundle summary (UC-8).
- Opt-in write tools (UC-3, UC-4, UC-6) with `dryRun` support. Separately gated destructive tools (UC-5).
- The server acts only with the invoking user's Kubernetes credentials. It never reads Secret or ConfigMap contents.
- Resources for the live OpenAPI schema. Prompts for "troubleshoot VM" and "create VM".
- stdio transport. Optional loopback-only Streamable HTTP for hosts that require HTTP.

**Non-goals (v1)**
- An in-cluster or multi-tenant deployment on the Supervisor.
- Any direct vCenter / govmomi access.
- Web console tickets (`VirtualMachineWebConsoleRequest`). They are credential-equivalent. Deferred.
- Creating or reading Secrets, for example to write a cloud-init payload. Bootstrap references existing Secrets only.
- v1alpha1–v1alpha5 APIs. Only the current storage/serving version (`vmopv1` = v1alpha6) is used.
- LLM-side logic. The server is deterministic and does not call any model.

---

## 7. How to achieve it

### 7.1 Topology options

| | **A. Client-side stdio binary (recommended v1)** | **B. In-cluster Streamable HTTP service** |
|---|---|---|
| Where it runs | User's workstation; the host launches it | Deployment on the Supervisor control plane |
| Identity | User's kubeconfig (vSphere/VCF CLI token) | Must map an MCP OAuth token to a Supervisor identity |
| RBAC | Inherits the user's RBAC exactly | Needs impersonation (a highly privileged SA) or token exchange |
| New attack surface | None on the cluster | New network endpoint, authn/authz, multi-tenant isolation, and DoS surface |
| Ship vehicle | Release artifact / `make` target | Operator image, manifests, WCP packaging, upgrade path |
| E2E impact | Not cluster-observable | Cluster-observable; full E2E required |

**Recommendation: A for v1. B is re-evaluated as its own spec later.**

### 7.2 Why B is deferred

The MCP authorization spec requires that a server **not pass through** tokens it receives from the client to upstream APIs. It must validate that tokens were issued *for itself*, which prevents confused-deputy attacks. An in-cluster server therefore cannot forward the host's bearer token to the kube-apiserver. It would need one of the following:

- **(i)** impersonation (`impersonate` verbs on users and groups), which is high privilege and needs strict audit,
- **(ii)** RFC 8693 token exchange with the Supervisor / VCF identity broker, or
- **(iii)** a Supervisor-native OAuth resource-server registration.

Each is a cross-team design (identity, WCP packaging) that should not block v1's value.

### 7.3 Hard boundaries

1. **Kubernetes API only.** No govmomi, no vCenter credentials, no `pkg/providers/vsphere`. This matches the constitution rule that nothing outside the provider calls vSphere, and it keeps the binary small.
2. **User identity only.** No service-account fallback, no stored credentials.
3. **No Secret or ConfigMap reads.** Bootstrap references are reported as `{kind, name, key, exists}`. `exists` comes from a `get` whose returned body is discarded. If the user lacks `get secrets`, `exists` is reported as `unknown`. If that is still deemed too revealing, the server reports only the reference.
4. **Output redaction layer.** Strip `metadata.managedFields` and the `kubectl.kubernetes.io/last-applied-configuration` annotation. Summarize `spec.bootstrap` (provider type plus Secret references only). Inline sysprep or vApp property values are masked.

### 7.4 Safety model for tools

| Tier | Enabled by | Annotations | Examples |
|---|---|---|---|
| Read | default | `readOnlyHint: true`, `openWorldHint: false` | `list_*`, `get_*`, `diagnose_virtual_machine`, `get_events` |
| Write | `--enable-write` | `readOnlyHint: false`, `destructiveHint: false`, idempotent where true | power state, restart, resize, create VM / snapshot / service / publish request, scale RS |
| Destructive | `--enable-destructive` (implies write) | `destructiveHint: true` | delete VM, delete snapshot, revert snapshot |

Rules:
- Tools for disabled tiers are **not registered**, so they are not in `tools/list`. They are not merely rejected at call time.
- Every write or destructive tool accepts `dryRun` (default `false`) and maps it to Kubernetes server-side dry-run, so admission webhooks run for real.
- Mutations use a **patch with optimistic locking** (resourceVersion precondition) and field manager `vmop-mcp`. They never do a blind full-object update.
- Optional namespace allow-list (`--namespaces a,b`). Tools reject other namespaces before calling the API.
- Hosts are expected to prompt for human approval on non-read-only tools. The server's annotations drive that UX.

### 7.5 Tool catalog (v1)

All names use snake_case. `namespace` defaults to the kubeconfig context's namespace. List tools accept `labelSelector`, `limit`, and `continue`.

**Read tier**

| Tool | Returns |
|---|---|
| `whoami` | Current context, server, namespace, and user and groups (via `SelfSubjectReview`); enabled tiers |
| `check_access` | `SelfSubjectAccessReview` for a verb and resource in a namespace |
| `list_virtual_machines` / `get_virtual_machine` | Summary: name, powerState (spec and status), class, image, primary IPs, zone, Ready/Created conditions, age. `get` adds a sanitized spec and status. |
| `diagnose_virtual_machine` | Structured findings (UC-1) |
| `describe_virtual_machine_bundle` | Redacted support summary (UC-8) |
| `list/get_virtual_machine_classes` | Hardware (CPU, memory), reserved profile, description |
| `list/get_virtual_machine_images` | Namespace and cluster images: name, OS info, firmware, HW version, Ready |
| `list/get_virtual_machine_services` | Type, ports, selector, LB IP |
| `list/get_virtual_machine_snapshots` | Target VM, memory/quiesce, Ready, and the VM's current snapshot |
| `list/get_virtual_machine_publish_requests` | Source, target, state |
| `list/get_virtual_machine_groups`, `list/get_virtual_machine_replica_sets` | Members / replicas, readiness |
| `get_events` | Events for a named object, newest first, capped |

**Write tier**

`create_virtual_machine`, `set_virtual_machine_power_state`, `restart_virtual_machine`, `resize_virtual_machine`, `create_virtual_machine_snapshot`, `create_virtual_machine_service`, `create_virtual_machine_publish_request`, `scale_virtual_machine_replica_set`.

**Destructive tier**

`delete_virtual_machine`, `delete_virtual_machine_snapshot`, `revert_virtual_machine_snapshot`.

**Resources**
- `vmop://openapi/v1alpha6/{kind}`: the schema for the kind, taken from the cluster's `/openapi/v3/apis/vmoperator.vmware.com/v1alpha6`. It is readable by any authenticated user through `system:discovery`.
- `vmop://docs/conditions`: static reference for VM condition types and their meaning.

**Prompts**
- `troubleshoot-vm(namespace, name)`
- `create-vm(namespace)`

### 7.6 Code layout and module

Isolate the MCP SDK and its transitive dependencies (`golang-jwt`, `oauth2`, `jsonschema-go`, `segmentio/encoding`) from the operator's dependency graph and vulnerability-scan surface by creating a **new Go sub-module**:

```
mcp/                              # module github.com/vmware-tanzu/vm-operator/mcp
  go.mod                          # requires .../api (replace => ../api)
  cmd/vmop-mcp/main.go            # flags, kubeconfig, transport selection
  pkg/server/                     # MCP server construction, tier gating
  pkg/tools/                      # tool handlers (one file per resource kind)
  pkg/diagnose/                   # VM diagnosis aggregator (pure logic)
  pkg/redact/                     # output sanitization
  pkg/kube/                       # client factory, kubeconfig reload, errors
  pkg/resources/, pkg/prompts/
```

- The module imports **only** the `api` module (`vmopv1` alias) plus controller-runtime client and client-go. It does **not** import the root module, which avoids pulling in govmomi and the manager.
- Existing Makefile module discovery (`GO_MOD_FILES`) picks up the module for `modules` and `lint-go`. It needs a dedicated `test-mcp` target, like `test-api`, and must be excluded from root `COVERED_PKGS`.
- Build: `make vmop-mcp` / `vmop-mcp-only`, modeled on `web-console-validator-only`, cross-compiled for darwin, linux, and windows on amd64 and arm64. It is **not** added to the operator image or Dockerfile.

### 7.7 Testing

- **Unit**: tool handlers run against a controller-runtime fake client, with a client-to-server round-trip over the SDK's in-memory transport. Tests cover tier gating, redaction, diagnosis rules, dry-run propagation, and namespace allow-listing.
- **Integration (envtest)**: real kube-apiserver with VM Operator CRDs. Exercises RBAC denial, SSAR/SSR, optimistic-lock conflicts, and server-side dry-run.
- **E2E**: a client-side binary does not change cluster-observable behavior, so a full E2E is not mandatory under `e2e-sync-with-changes.md`. A **light, read-only** E2E is still recommended. It runs `vmop-mcp` against a real Supervisor as the DevOps user and calls `whoami`, `list_virtual_machines`, and `diagnose_virtual_machine` on a VM created by the existing suite. This catches real RBAC and OpenAPI discovery differences that envtest cannot.
- A new test label (for example `testlabels.MCP`) is added to `pkg/constants/testlabels`.

### 7.8 Constitution considerations

| Item | Position |
|---|---|
| Thin controllers / provider-only vSphere | N/A / honored. No controllers. No vSphere calls. |
| API compatibility | No CRD changes. The tool contract is versioned independently (server `Implementation.Version` = build version; tool names stable). |
| Feature flag (`pkgcfg.Features`) | Not applicable: the binary runs outside the manager and is opt-in by installation. Tier flags (`--enable-write`, `--enable-destructive`) are the gates. Record in Complexity tracking. |
| New sub-module | Add a row to the constitution's module table in the same PR. |
| Importas / headers / markdown | Standard. `vmopv1` → v1alpha6. |
| One test file per package | Honored per package under `mcp/pkg/*`. |

### 7.9 Phasing

1. **Phase 1 (MVP)**: module scaffold; read tier; `diagnose_virtual_machine`; redaction; stdio; unit and envtest tests.
2. **Phase 2**: write tier with dry-run; resources and prompts; loopback Streamable HTTP.
3. **Phase 3**: destructive tier; support bundle; read-only E2E; docs (`docs/` page for configuring hosts).
4. **Later (separate spec)**: in-cluster Streamable HTTP with Supervisor identity integration; web console tool; VKS-aware tools.

---

## 8. Risks and mitigations

| Risk | Mitigation |
|---|---|
| Prompt injection via object data (for example, a VM annotation saying "delete all VMs") | Read-only by default. Destructive tier separately gated. Host approval prompts. Tool descriptions state that tool output is data, not instructions. |
| Credential leakage | No Secret reads, redaction layer, no web-console tickets in v1. |
| Tool-contract drift as the API evolves (v1alpha7) | Tools bind to `vmopv1`. The alias bump is part of the API-version bump checklist. Output schemas are covered by golden tests. |
| Dependency and CVE surface | Sub-module isolation. `vulncheck-go` covers the module. |
| Large outputs overflow model context | Summaries by default, `limit`/`continue`, event caps, and a `verbose` flag for full sanitized objects. |

---

## 9. Open questions

- [NEEDS CLARIFICATION: Binary name and module path. Proposed: `vmop-mcp` and `github.com/vmware-tanzu/vm-operator/mcp`.]
- [NEEDS CLARIFICATION: Distribution channel. GitHub release assets (no release workflow exists today), or bundling with the VCF CLI plugin set?]
- [NEEDS CLARIFICATION: Should bootstrap Secret *existence* checks be performed, given that some DevOps roles lack `get secrets`?]
- [NEEDS CLARIFICATION: Does the Supervisor's DevOps role grant `get` on `/openapi/v3`? It is expected through `system:discovery`; verify on a real Supervisor.]
- [NEEDS CLARIFICATION: Epic ticket to be filed.]
