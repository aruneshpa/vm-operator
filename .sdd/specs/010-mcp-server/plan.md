# Implementation Plan: MCP Server for VM Operator

- **Spec**: [`spec.md`](./spec.md)
- **Model**: [`model.md`](./model.md)
- **Epic**: TBD
- **Date**: 2026-09-30

## Summary

Ship `vmop-mcp`, a client-side MCP server binary that runs over the stdio transport. It is built from a **new Go sub-module** `mcp/` (`github.com/vmware-tanzu/vm-operator/mcp`) on the official MCP Go SDK. It talks only to the Supervisor Kubernetes API, using the user's kubeconfig. Every output is an allow-list projection of `vmopv1` objects into curated DTOs. It never touches Secrets or ConfigMaps.

Increment 1 delivers the read tier: identity, inventory, events, diagnosis, and explain, with a read-only Supervisor E2E. Increment 2 adds the opt-in write tier: power, restart, wait, storage discovery, and create with preflight and dry run.

There is no change to controllers, webhooks, CRDs, the manager, or the operator image. Design decisions and the architecture review are recorded in [`research.md`](./research.md) and [`review.md`](./review.md).

## Technical context

- **Go version**: 1.26.8 (root `go.mod`). The SDK requires Go ≥ 1.25.
- **API version touched**: none changed. The binary consumes `vmopv1` = `api/v1alpha6` (read and write through the API server).
- **Modules touched**:
  - new `mcp/`
  - root `Makefile`
  - `.github/workflows/ci.yml`
  - `.github/dependabot.yaml`
  - `pkg/constants/testlabels` (new label)
  - `test/e2e` (new specs; `require` + `replace` of `../../mcp`)
- **New dependencies** (in `mcp/go.mod` only):
  - `github.com/modelcontextprotocol/go-sdk v1.8.0`, which transitively brings `google/jsonschema-go`, `golang-jwt/jwt/v5`, `golang.org/x/oauth2`, `segmentio/encoding`, `yosida95/uritemplate/v3`, and `golang.org/x/time`;
  - `sigs.k8s.io/controller-runtime`, `k8s.io/client-go`, `k8s.io/api`, and `k8s.io/apimachinery`, pinned to the same versions as root (currently `v0.37.0` / controller-runtime `v0.25.0`);
  - `github.com/vmware-tanzu/vm-operator/api`, `.../external/capabilities`, and `.../pkg/constants/testlabels`, via `replace => ../...`;
  - `github.com/onsi/ginkgo/v2` and `github.com/onsi/gomega`, for tests only.
- **Root module**: gains no new dependencies.

## Constitution check

| Rule | Status | Notes |
|------|--------|-------|
| API compatibility (no breaking CRD change) | OK | No CRD or API change. The tool contract is versioned separately (`model.md` §1). |
| Every new CRD has markers / generated manifests | N/A | No new CRD. |
| Thin controllers; business logic in `pkg/` | N/A | No controllers. Logic lives in `mcp/pkg/*`; `mcp/cmd/vmop-mcp` is flags and wiring only. |
| No vSphere API calls outside `pkg/providers/vsphere` | OK | The module imports no govmomi and does not import the root module. This is enforced by a depguard rule in `.golangci.yml` scoped to `mcp/**` (T004). |
| `observedGeneration` / `Ready` condition | N/A | No reconciler. |
| Fan-out patch semantics (`MergeFromWithOptimisticLock`) | OK | All writes use a merge patch with optimistic lock, skip when unchanged, and bound retries (model.md §7). |
| Webhooks | N/A | None added. |
| One test file + one suite file per package; external `_test` package | OK | Each `mcp/pkg/<x>` has `<x>_suite_test.go` + `<x>_test.go`. Tool families are split into separate packages so the rule does not produce one giant test file. |
| Labels from `pkg/constants/testlabels` | OK | New `testlabels.MCP`. Unit specs use `Label(testlabels.MCP)`. Envtest specs use `Label(testlabels.MCP, testlabels.EnvTest)`. |
| E2E coverage for Supervisor-observable change | OK (voluntary) | No cluster-observable behavior changes. A read-only E2E (Increment 1) and a write E2E (Increment 2) still run against a real Supervisor as a DevOps user (spec G-25), because envtest cannot exercise VM Operator admission or Supervisor RBAC. |
| importas aliases / goimports local-prefixes / depguard | OK | `vmopv1` → `api/v1alpha6`; `capv1` for capabilities; `ctrlclient`, `metav1`, and `corev1` as in `.golangci.yml`. The root `k8s.io/utils` deny (which points to root `pkg/util/ptr`) is satisfied with Go 1.26 `new(expr)` or a local helper. |
| Copyright header on every Go file | OK | Standard Broadcom header. |
| Markdown not hard-wrapped | OK | — |
| No internal URLs; `vmop-NNN` / `WIKI page` | OK | — |
| Feature flag (`pkgcfg.Features.*`) for spec code | **Exception** | See Complexity tracking. |
| Sub-module listed in constitution module table | OK | Row added in T005. Missing existing rows are backfilled at the same time (documentation-only amendment, called out in the PR). |
| Spec ↔ epic, task ↔ story tickets | **Pending** | `Epic: TBD`. Tickets must be filed before the spec PR merges. |

## Project structure

```
mcp/                                   # NEW module .../vm-operator/mcp
├── go.mod / go.sum
├── README.md                          # dev notes; user docs live in docs/
├── cmd/vmop-mcp/main.go               # flags, logging→stderr, stdio run
├── pkg/buildinfo/                     # Version/Commit set by -ldflags
├── pkg/kube/                          # kubeconfig load/reload, client
│                                      # factory, static RESTMapper,
│                                      # guard (no Secret/ConfigMap),
│                                      # served-version check
├── pkg/capabilities/                  # feature detection (VM snapshots)
├── pkg/contract/                      # DTOs, contract.Version, cursor,
│                                      # ToolError codes
├── pkg/projection/                    # vmopv1 → DTO allow-list
│                                      # projection, BootstrapSummary
├── pkg/toolkit/                       # Registrar (tiers, caps,
│                                      # annotations), namespace policy,
│                                      # result/size-cap, error mapping
├── pkg/server/                        # New(Options) → *mcp.Server; wires
│                                      # all tool packages; golden file
├── pkg/tools/identity/                # whoami, check_access
├── pkg/tools/vm/                      # list/get VMs
├── pkg/tools/vmclass/                 # list/get classes
├── pkg/tools/vmimage/                 # list/get VMI+CVMI, merged cursor
├── pkg/tools/vmsnapshot/              # list/get snapshots (cap-gated)
├── pkg/tools/events/                  # get_events
├── pkg/diagnose/                      # pure rule engine (no MCP)
├── pkg/tools/diagnose/                # diagnose_virtual_machine tool
├── pkg/openapi/                       # /openapi/v3 fetch, cache, field
│                                      # lookup
├── pkg/tools/explain/                 # explain_field tool
├── pkg/prompts/                       # troubleshoot-vm, create-vm
├── pkg/tools/vmpower/                 # Increment 2: power, restart, wait
├── pkg/tools/storage/                 # Increment 2: list_storage_classes
├── pkg/tools/vmcreate/                # Increment 2: create + preflight
└── test/envtest/                      # envtest helper (no ginkgo import)

pkg/constants/testlabels/test_labels.go   # + MCP label
Makefile                                  # vmop-mcp(-only), test-mcp,
                                          # vulncheck-mcp
Dockerfile.e2e                            # COPY mcp/ (E2E replace target)
.github/workflows/ci.yml                  # test row, build row, vulncheck
.github/dependabot.yaml                   # + /mcp
.golangci.yml                             # depguard rule for mcp/**
test/e2e/go.mod                           # require/replace ../../mcp
test/e2e/vmservice/vmservice/mcp/         # NEW E2E package
test/e2e/vmservice/vmservice_test.go      # + Context("MCP-SERVER")
docs/guides/mcp-server/README.md          # user guide + compat matrix
mkdocs.yml                                # nav entry
```

**Module precedent.** `test/e2e` is a separate module with `replace` directives, its own CI row, and a Dependabot entry. `mcp/` follows the same pattern. `cmd/web-console-validator` is precedent only for the "thin main + logic package" shape.

**Build info.** Root `BUILDINFO_LDFLAGS` targets root `pkg` variables, which the module cannot import. The `vmop-mcp-only` target passes `-X github.com/vmware-tanzu/vm-operator/mcp/pkg/buildinfo.Version=…` and `…Commit=…` instead.

## Key design rules

These rules are binding for implementers.

1. **DTO rule.** No `vmopv1`, `metav1.ObjectMeta`, `metav1.Condition`, or `resource.Quantity` type appears in any tool `In` or `Out` type. SDK schema inference panics on or mis-types them (review R-7). All times and quantities are strings. Use `jsonschema:"…"` struct tags for descriptions and enums.
2. **Projection rule.** `mcp/pkg/projection` copies fields explicitly into DTOs. Nothing is copied by reflection or by default. Bootstrap becomes `BootstrapSummary`. `extraConfig` becomes keys only. Class `configSpec` becomes `hasConfigSpec`. Image `ovfProperties` becomes keys only.
   - A field-inventory test (T014) parses `api/v1alpha6` with `go/parser` and classifies every field under `VirtualMachineSpec.Bootstrap`, `VirtualMachineSpec.Advanced`, `VirtualMachineStatus`, `VirtualMachineClassSpec`, and `VirtualMachineImageStatus`. Each field is classified as `projected`, `summarized`, or `excluded`.
   - The test fails when a field appears that is not in the inventory. This forces an explicit decision on every API addition.
3. **Secret guard.** The client handed to tools is wrapped by `kube.GuardedClient`, which returns an error for any `Secret` or `ConfigMap` GVK (typed or unstructured) before sending. Tests also install a controller-runtime `interceptor.Funcs` on the fake client that fails the spec on any such request.
4. **Tier and capability registration.** Tool packages expose `Register(r *toolkit.Registrar)`. `Registrar.AddRead` and `Registrar.AddWrite` set all four annotations explicitly (model.md §3). `AddWrite` is a no-op unless `--enable-write` is set. `Registrar.Gated(capName, …)` registers only when the capability is `active`.
5. **Output rule.** The `toolkit` result helper sets `Content` to a short text summary, so the SDK does not duplicate the JSON. It sets `StructuredContent` to the DTO and enforces the 64 KiB cap with truncation semantics.
6. **Namespace policy.** `toolkit.NamespacePolicy` resolves the default namespace (from `--namespace`, otherwise the kubeconfig context) and enforces `--namespaces`. It returns `namespace_not_allowed` before any API call. It exempts cluster-scoped kinds.
7. **Error mapping.** `toolkit.MapError` maps `apierrors` to the codes in model.md §5. The Supervisor message is passed through verbatim.
8. **stdio hygiene.** All logging (`klog`, controller-runtime `ctrllog`) goes to stderr. `rest.Config.WarningHandler` is a stderr logger.
   - A kubeconfig whose `exec` plugin sets `interactiveMode: Always` is rejected at startup with a clear message.
   - A test builds the binary, runs it with a fake kubeconfig against an envtest API server over `mcp.CommandTransport`, and asserts that the handshake succeeds. This proves nothing else writes to stdout.
9. **Credential reload.** `kube.ClientProvider.Get(ctx)` returns a cached client. It rebuilds the client when the kubeconfig file's mtime changes, or once after a 401 within a single tool call, then retries that call once. The RESTMapper is static (`meta.NewDefaultRESTMapper`) for the known kinds, so a rebuild needs no discovery.
10. **Served-version check.** At startup, discovery must list `vmoperator.vmware.com/v1alpha6`, or the process exits non-zero with the served versions on stderr. The served list is kept for `whoami`.
11. **Writes.** Every write:
    - reads the target and evaluates guards;
    - builds the patch from `DeepCopy`;
    - sends `ctrlclient.MergeFromWithOptions(base, ctrlclient.MergeFromWithOptimisticLock{})` with `ctrlclient.FieldOwner("vmop-mcp")`;
    - skips the write if the spec is semantically equal (`apiequality.Semantic.DeepEqual`);
    - re-runs the whole read-guard-patch cycle up to 3 times on 409.

    `dryRun` maps to `ctrlclient.DryRunAll`. The response object, which reflects the mutating webhooks, is what gets projected back.

## Capability detection

- **Source (proposed)**: the cluster-scoped `capabilities.iaas.vmware.com/v1alpha1` object `supervisor-capabilities`, `status.supervisor["supports_VM_service_VM_snapshots"].activated`. This is the same key VM Operator uses (`pkg/config/capabilities/capabilities.go:61`).
- **If the Get is forbidden or the object is absent**: the state is `unknown`. Snapshot read tools are **still registered**, because they are read-only and harmless on an unreconciled kind. Their descriptions note that the feature may be disabled.
- **Increment 2**: write tools for gated kinds are out of scope, so no gated write tool depends on this.
- This is blocked on the spec clarification about DevOps access to `Capabilities` (T030).

## API / CRD strategy

There are no CRD changes. The binary is compiled against `vmopv1` (v1alpha6). When VM Operator bumps the active alpha (the `vmopv1` alias in `.golangci.yml`), the MCP module's `builtForVersion`, projections, and field inventory must be updated in the same change. That change is a major contract bump if any DTO field changes meaning.

## Controller / webhook impact

None. There is no new RBAC in `config/`. The tools rely on the user's existing Supervisor RBAC. The docs list the verbs used per tool (from the `check_access` table in T091).

## Test strategy

- **Unit** (`Label(testlabels.MCP)`): these use a controller-runtime fake client from `sigs.k8s.io/controller-runtime/pkg/client/fake`, with `WithStatusSubresource` for VM Service kinds and a Secret/ConfigMap interceptor. An MCP client–server pair is connected over `mcp.NewInMemoryTransports()`. Coverage:
  - projections and the field inventory;
  - cursor encode/decode and 410 mapping;
  - the size cap;
  - the annotation table;
  - tier and capability registration;
  - the namespace policy;
  - every diagnose rule;
  - write guards, the no-op skip, and 409 retry via interceptor;
  - dry-run propagation;
  - the `tools/list` golden file (`-update` flag to regenerate).
- **Integration** (`Label(testlabels.MCP, testlabels.EnvTest)`): `mcp/test/envtest` starts envtest with CRDs from `../config/crd/bases` and `../config/crd/external-crds/iaas.vmware.com_capabilities.yaml`. It resolves CRD paths from the repo root via `runtime.Caller`, and creates real authenticated users with `Environment.AddUser` plus namespaced RBAC and a kubeconfig file. Coverage:
  - real 403s;
  - `SelfSubjectReview` / `SelfSubjectAccessReview`;
  - optimistic-lock conflicts;
  - CRD-schema-level dry-run rejection;
  - `/openapi/v3` explain against real CRD schemas;
  - kubeconfig mtime reload;
  - the stdio binary handshake.
  - envtest runs **without** VM Operator webhooks, because they live in the root module. VM Operator admission behavior is covered only in E2E.
- **E2E** (`test/e2e/vmservice/vmservice/mcp/`, labels `Label("mcp")`, `Label("devops")`): these reuse `testutils.CreateUserAndLogin` and `testutils.SetUserPermissionsOnNamespace` (the pattern in `test/e2e/vmservice/vmservice/devops/namespaces.go`) to get a DevOps-user kubeconfig. They build the server in-process via `mcp/pkg/server.New` and connect over in-memory transports.
  - **Increment 1**: `whoami`, `list_virtual_machines`, and `diagnose_virtual_machine` on the suite's existing Linux VM; `explain_field` (records whether `/openapi/v3` is readable, resolving the spec clarification); namespace-policy refusal. Asserts that the audit-relevant identity equals the DevOps user.
  - **Increment 2**: `create_virtual_machine{dryRun:true}` with a bad class (preflight) and a valid spec (admitted, nothing persisted); a real create; `wait_for_virtual_machine`; power off/on; group-guard refusal where VM groups are enabled.

## Rollout / migration

- **Feature flag**: none (see Complexity tracking). Users opt in by installing the binary. The write tier is opt-in via `--enable-write`.
- **Distribution (proposed; pending clarification)**:
  - release binaries only for darwin, linux, and windows on amd64 and arm64, built by `make vmop-mcp-only GOOS=… GOARCH=…`;
  - tagged `mcp/vX.Y.Z`, following the existing `api/vX.Y.Z` convention;
  - `go install` is not supported while `mcp/go.mod` uses `replace`.
- **Compatibility matrix**: published in `docs/guides/mcp-server/README.md`. Each `vmop-mcp` release lists its `builtForVersion` and the minimum VM Operator release that serves it.
- **Release note**: "Add vmop-mcp, an MCP server that lets AI assistants inspect and diagnose VM Service resources using the user's own Supervisor credentials."

## Complexity tracking

| Violation | Why needed | Simpler alternative rejected because |
|-----------|------------|--------------------------------------|
| No `pkgcfg.Features` gate for spec code (AGENTS.md "not guarded by a feature flag") | `vmop-mcp` runs outside the controller manager on a user's workstation. `pkgcfg` is manager-context configuration and is not available there. The effective gates are installing the binary and passing `--enable-write`. | Adding a manager FSS would gate nothing. The binary never reads manager config. T005 adds a note to `AGENTS.md` so AI reviewers do not flag `mcp/**`. |
| New Go sub-module (fourth independent k8s dependency set) | Keeps the MCP SDK, JWT, and OAuth dependencies out of the operator binary's graph and vulnerability surface. Lets the tool version on its own. | Adding it to the root module would pull the SDK into the manager's `go.mod` and CVE scope for a binary that does not ship in the image. Pinning k8s versions to root (T001) and Dependabot grouping limit the drift. |
| E2E for a non-cluster-observable change | Only a real Supervisor exercises VM Operator admission (dry run), Supervisor RBAC, and `/openapi/v3` access. | Envtest-only coverage would leave the highest-risk assumptions untested (review R-5). |
