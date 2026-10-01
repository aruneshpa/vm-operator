# Research: MCP Server for VM Operator

- **Spec**: [`spec.md`](./spec.md)
- **Design record**: [`proposal.md`](./proposal.md) → [`review.md`](./review.md)
- **Date**: 2026-09-30

## 1. Process

1. A design proposal ([`proposal.md`](./proposal.md)) covered benefits, use cases, scenarios, topology options, a safety model, a tool catalog, a module layout, and phasing.
2. An independent architecture review ([`review.md`](./review.md)) checked each claim against this repository at `efcdb7de1` and against the MCP Go SDK v1.8.0 source. **Verdict: Approve with changes.** It raised 3 blocking, 9 major, and 12 minor or nit findings.
3. The spec, plan, model, and tasks incorporate every blocking and major finding. The table in §4 shows where each one landed.

## 2. MCP and the Go SDK

- **SDK**: `github.com/modelcontextprotocol/go-sdk`. The latest stable version is v1.8.0 (`go list -m -versions`, 2026-09-30). Its `go.mod` declares `go 1.25.0`; this repository uses `go 1.26.8`.
- **Direct dependencies**: `golang-jwt/jwt/v5`, `google/go-cmp`, `google/jsonschema-go v0.4.3`, `segmentio/encoding`, `yosida95/uritemplate/v3`, `golang.org/x/oauth2`, `golang.org/x/time`, `golang.org/x/tools`.
- **APIs used**:
  - `mcp.NewServer`, `mcp.AddTool[In, Out]`, `mcp.StdioTransport`, `mcp.CommandTransport`, `mcp.NewInMemoryTransports`, and `ToolAnnotations`, in `mcp/protocol.go:1964-2003` (the protocol types file inside the SDK).
  - `AddTool` infers JSON Schemas from Go types and **panics** if inference fails (`mcp/server.go:603-609`). It validates call arguments against the inferred schema. This is why the plan requires curated DTOs (review R-7).
  - `ToolAnnotations`: a nil `DestructiveHint` or a nil `OpenWorldHint` means `true`. Every hint must be set explicitly (review R-13).
  - When `Content` is unset, the SDK fills it with the JSON of `Out` in addition to `StructuredContent`, so the output is sent twice (`mcp/tool.go:46-48`; review R-14).
  - The Streamable HTTP handler protects against DNS rebinding by default, but has no cross-origin protection and no caller authentication (`mcp/streamable.go:174-195`). For that reason, v1 uses stdio only (review R-3).
- **MCP authorization**: a server must not pass through tokens that the client gave it to upstream APIs. This rules out a simple in-cluster deployment that forwards the host's bearer token to kube-apiserver. The alternatives are impersonation, RFC 8693 token exchange, or a Supervisor-native OAuth resource server. Each is a cross-team identity design, so the in-cluster topology is deferred to a future spec.

## 3. Repository evidence

| Topic | Evidence |
|---|---|
| Thin-binary shape | `cmd/web-console-validator/main.go`: a controller-runtime client with the `vmopv1` scheme and no manager. It lives in the root module and ships in the image (`Makefile:889`), so it is precedent only for the code shape. |
| Separate-module precedent | `test/e2e/go.mod` has `replace` directives. It has its own CI row (`.github/workflows/ci.yml` `build-image` matrix, `dir: test/e2e`) and a Dependabot entry (`.github/dependabot.yaml`). |
| Module discovery | `Makefile` `GO_MOD_FILES` / `GO_MOD_DIRS` feed `lint-go` and `modules`. `vulncheck-go` runs only `./...` at the root. CI test rows set `covered-pkgs` explicitly. |
| Test labels | `pkg/constants/testlabels/test_labels.go` has no MCP label; T002 adds one. |
| Bootstrap readiness without Secret reads | `pkg/providers/vsphere/vmprovider_utils.go:339-344,381`: the VM controller sets `VirtualMachineBootstrapReady=False` with NotFound or `RequiredKeyNotFound`. |
| Inline payload fields | See the `api/v1alpha6` files below. |
| Class resize | `webhooks/virtualmachine/validation/virtualmachine_validator.go:709-731`: `className` is immutable unless `VMResizeCPUMemory`, and there is a `spec.class` instance-ownership check. Resize is out of scope for this spec. |
| Class existence is not checked at admission | `validateClassOnCreate` (`virtualmachine_validator.go:652-675`). This is why create runs preflight checks before the dry run. |
| Image immutability | `virtualmachine_validator.go:267-293`: `spec.image` and `spec.imageName` are both immutable on update. |
| Restart semantics | `api/v1alpha6/virtualmachine_types.go:1004-1016`, and the mutator converts `"now"` to a timestamp. Restart is not idempotent. |
| Group power control | `api/v1alpha6/virtualmachinegroup_types.go:105-150`. This is the basis for the group guard. |
| Feature-gated controllers | `controllers/controllers.go:63-87`, `webhooks/webhooks.go:78` |
| Capability key | `pkg/config/capabilities/capabilities.go:32,61`: `supervisor-capabilities`, `supports_VM_service_VM_snapshots`. The Capabilities CRD is in `config/crd/external-crds/iaas.vmware.com_capabilities.yaml`. |
| OpenAPI size | The v1alpha6 VM schema alone is about 5,000 lines of CRD YAML. This is why `explain_field` looks up individual fields instead of returning a whole-kind resource. |
| DevOps-user E2E identity | `test/e2e/vmservice/vmservice/devops/namespaces.go` uses `testutils.CreateUserAndLogin` and `testutils.SetUserPermissionsOnNamespace`. |

Inline payload fields in `api/v1alpha6`:

- `cloudinit/cloudconfig.go`: `runcmd`, `write_files[].content`, `sshAuthorizedKeys`
- `virtualmachine_bootstrap_types.go`: `scriptText`, vApp `properties`
- `sysprep/sysprep.go`: `guiRunOnce`, `scriptText`
- `virtualmachine_types.go`: `spec.advanced.extraConfig` (line 1357) and `status.extraConfig` (line 1563)
- `virtualmachinereplicaset_types.go`: `spec.template`, which embeds a full `VirtualMachineSpec`

## 4. Review findings → disposition

| Finding | Disposition |
|---|---|
| R-1 Secret "exists" probe | **Adopted.** No Secret or ConfigMap requests at all (spec G-2). A guard and a test trap enforce this (T012). Bootstrap readiness comes from VM conditions. |
| R-2 Deny-list redaction | **Adopted.** Allow-list projection plus a field-inventory test (plan rule 2, T014). No verbose mode. |
| R-3 Loopback HTTP without authn | **Adopted.** stdio only. All network transports are a non-goal. |
| R-4 Resize semantics | **Deferred.** Resize is a non-goal. The findings are recorded here for the follow-up spec. |
| R-5 Dry-run overstated | **Adopted.** Preflight, then dry run, then project the response (G-21, T072). Envtest limits are documented. The Supervisor E2E is in Increment 1 (T060). |
| R-6 Feature-gated kinds | **Adopted.** Registration is capability-aware (T030, T031). The access questions are open in the spec. |
| R-7 Raw types break schema inference | **Adopted.** DTO rule (plan rule 1). T013 checks that `jsonschema.For` succeeds for every DTO. |
| R-8 Build and CI gaps | **Adopted.** T003 (vulncheck across modules, `test-mcp`), T004 (CI row, build row, Dependabot, depguard). Distribution is binaries only for now, pending clarification. |
| R-9 Ownership and power modes | **Adopted** for power and restart (group guard, mode mapping, non-idempotent restart, 409 retry). Delete, resize, and scale are deferred. |
| R-10 OpenAPI too large | **Adopted.** Field-scoped `explain_field`. The embedded-CRD fallback was **rejected**: it would need a checked-in copy of more than 1 MB of CRDs inside the module. A clear `schema_unavailable` error is used instead. |
| R-11 Version skew | **Adopted.** Served-version check at startup (T011), and a compatibility matrix in the docs (T091). |
| R-12 Host approval is not a control | **Adopted** in part: untrusted-string marking and constant-only suggestions. Destructive tools, which would need server-side confirmation, are a non-goal. The follow-up spec must include `confirm` or a plan-token flow. |
| R-13 Hint defaults | **Adopted.** Explicit hints and an annotation-table test (T015). |
| R-14 Double encoding and size | **Adopted.** Text summary in `Content`, and a 64 KiB cap. |
| R-15 Pagination | **Adopted.** Opaque two-phase cursor and `cursor_expired`. |
| R-16 Condition coverage | **Adopted.** Coverage test parses the API constants (T041). The static conditions resource was dropped. |
| R-17 stdio hygiene | **Adopted.** Plan rule 8, T019. |
| R-18 depguard in the sub-module | **Adopted.** `new(expr)` or a local helper. A new depguard rule for `mcp/**` (T004). |
| R-19 Publish and storage discovery | Publish is **deferred**. Storage discovery is adopted via ResourceQuota, pending clarification (T071). |
| R-20 S-1 correction and E2E module | **Adopted.** The spec states that recreate is the only path. `test/e2e/go.mod` requires the MCP module (T060). |
| R-21 Bookkeeping | **Adopted.** T005 backfills the module table and adds the AGENTS.md note. The proposal is kept as a design record, marked superseded. |
| R-22 Scope trim | **Adopted.** Increment 1 is read-only. Increment 2 is power, restart, wait, and create. Everything else is a non-goal or follow-up. |
| R-23 Contract versioning | **Adopted.** `model.md` §1, the golden `tools/list` file (T016). |
| R-24 Small facts | **Adopted.** `SelfSubjectReview` fallback, and cluster-scoped image lookups exempt from the allow-list. |

## 5. Follow-up specs (not in this spec)

1. **Destructive tier**: delete a VM or snapshot, revert to a snapshot. Requires server-side confirmation (`confirm: "<ns>/<name>"` or a plan-token flow), MCP elicitation when available, and replica-set ownership guards.
2. **Extended write operations**: resize (null `spec.class`, handle the `VMResizeCPUMemory` gate, report `PowerCyclePending`), snapshot create, replica-set scale via `/scale`, VM service create, publish request (content library discovery through `imageregistry.vmware.com`), and group operations.
3. **In-cluster and remote topology**: Streamable HTTP on the Supervisor, with a Supervisor identity integration (impersonation, token exchange, or native OAuth) and full E2E.
4. **Web console**: `VirtualMachineWebConsoleRequest` tickets. These are credential-equivalent and need their own threat model.
