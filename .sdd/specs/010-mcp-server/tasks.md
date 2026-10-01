# Tasks: MCP Server for VM Operator

- **Spec**: [`spec.md`](./spec.md)
- **Plan**: [`plan.md`](./plan.md)
- **Model**: [`model.md`](./model.md)
- **Epic**: TBD

> **Ticket tags.** `[vmop-TBD]` is a placeholder. File one story or sub-task per tagged task under the epic, and set Epic Link (`customfield_10830`) post-create. Replace each placeholder before the PR that implements the task merges.
>
> **Conventions for implementers.**
> - Every new Go file carries the Broadcom copyright header.
> - Every package has exactly one `<pkg>_suite_test.go` (only the `TestXxx` → `RunSpecs` entry point) and one `<pkg>_test.go`, both in package `<pkg>_test`.
> - Unit specs use `Label(testlabels.MCP)`. Envtest specs use `Label(testlabels.MCP, testlabels.EnvTest)`.
> - No `time.Sleep`.
> - Follow the "Key design rules" in `plan.md`. They are acceptance criteria for every task.
> - Run `make lint-go` and `make test-mcp` before opening a PR.

> **Implementation status (2026-09-30).** All Go tasks are implemented in `mcp/` and pass `make test-mcp` (331 specs, unit plus envtest) and `make lint-go`. Notes on what differs from the task text:
> - T003: `test-mcp` runs `hack/test.sh` with a new `GO_TEST_COVER=no` switch, because ginkgo's coverage finalization runs `go tool cover` from the root module and cannot see the `mcp` module. The CI `mcp` row uses `make-target: test-mcp`.
> - T030/T031 and T071 implement the proposed behavior. The spec clarifications that block them are still open.
> - T060/T080: the E2E specs compile and are wired under `Label("mcp")` and `Label("experimental")`. They have not run on a Supervisor, so they stay unchecked.
> - T090 builds release binaries plus SHA256SUMS. Publishing them waits on the distribution-channel clarification.
> - T092 stays open until the clarifications are resolved.

## Phase 1: Setup

- [x] T001 [vmop-TBD] Scaffold the module.
  - Files: `mcp/go.mod`, `mcp/go.sum`, `mcp/README.md`, `mcp/pkg/buildinfo/buildinfo.go`.
  - Module path: `github.com/vmware-tanzu/vm-operator/mcp`, `go 1.26.8`.
  - `require` `github.com/modelcontextprotocol/go-sdk v1.8.0`, and controller-runtime, client-go, api, and apimachinery at the root module's versions (`v0.25.0` / `v0.37.0`).
  - `replace` `github.com/vmware-tanzu/vm-operator/api => ../api`, `.../external/capabilities => ../external/capabilities`, and `.../pkg/constants/testlabels => ../pkg/constants/testlabels`.
  - `buildinfo` exposes `Version` and `Commit` vars for `-ldflags -X`.
- [x] T002 [P] Add `MCP = "mcp"` with a godoc comment in `pkg/constants/testlabels/test_labels.go`.
- [x] T003 [P] [vmop-TBD] Update the Makefile.
  - Add `VMOP_MCP := $(BIN_DIR)/vmop-mcp`.
  - Add a `vmop-mcp-only` target: `go -C mcp build -o $(abspath $(VMOP_MCP)) -ldflags "-X github.com/vmware-tanzu/vm-operator/mcp/pkg/buildinfo.Version=… -X …Commit=…" ./cmd/vmop-mcp`, honoring `GOOS`, `GOARCH`, and `CGO_ENABLED=0`.
  - Add `vmop-mcp: prereqs lint-go vmop-mcp-only`.
  - Add `test-mcp: | $(GINKGO) $(ETCD) $(KUBE_APISERVER)` running `COVERAGE_FILE="" hack/test.sh ./mcp/...`.
  - Add `-not -path './mcp/*'` to the default `COVERED_PKGS` so that `make test` stays root-only.
  - Add a `vulncheck-mcp` target running `$(GOVULNCHECK) -C mcp ./...` (the pinned govulncheck v1.7.0 supports `-C`), and make `vulncheck-go` depend on it. Scan only root and `mcp`; do not widen the scan to other modules in this PR, because unrelated existing findings there would block it.
  - Do **not** add the binary to `image-build` or the `Dockerfile`.
  - File: `Makefile`.
- [x] T004 [P] [vmop-TBD] Wire CI, Dependabot, and lint.
  - `.github/workflows/ci.yml`:
    - Add a test-matrix row `{name: mcp, covered-pkgs: ./mcp/..., make-target: test-nocover, upload-coverage: false}`.
    - Add a build smoke job that runs `make vmop-mcp-only` for `linux/amd64`, `darwin/arm64`, and `windows/amd64`.
    - Confirm that the `vulncheck` job picks up the new Makefile loop.
  - `.github/dependabot.yaml`: add `"/mcp"` to `directories`.
  - `.golangci.yml`:
    - Add a depguard rule, `files: ["**/mcp/**"]`, that denies `github.com/vmware/govmomi` and `github.com/vmware-tanzu/vm-operator/pkg`, `…/controllers`, `…/webhooks`, and `…/services`. Depguard matches by prefix, so the rule MUST explicitly allow `github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels`; otherwise every MCP test file is rejected.
    - Verify that `cd mcp && golangci-lint run` resolves the root config (via `make lint-go`).
- [x] T005 [P] Update the SDD and constitution documents.
  - `.sdd/memory/constitution.md`: add the `mcp/` row to the sub-module table, and backfill the missing rows (`test/e2e/`, `api-docs/`, `external/vim/api/`, `external/image-registry-operator/`, `external/mobility-operator/`). Call this out in the PR as a documentation-only amendment.
  - `AGENTS.md`: under "Do not flag", add that `mcp/**` code is exempt from the feature-flag rule per `.sdd/specs/010-mcp-server/plan.md` Complexity tracking.
  - `.sdd/INDEX.md`: flip the 010 status as work progresses.

## Phase 2: Foundational (blocks all user stories)

- [x] T010 [vmop-TBD] Build the kube client layer in `mcp/pkg/kube/` (`client.go`, `provider.go`, `kube_suite_test.go`, `kube_test.go`).
  - Kubeconfig loading via `clientcmd`: `--kubeconfig`, `$KUBECONFIG`, `--context`.
  - Reject `exec` plugins with `interactiveMode: Always`.
  - Scheme: `vmopv1`, `capv1`, `corev1` (Events, PVCs, ResourceQuotas only), `authenticationv1`, `authorizationv1`.
  - Static RESTMapper for the known kinds.
  - `rest.Config.WarningHandler` writes to stderr.
  - `ClientProvider.Get(ctx)` caches the client and rebuilds on kubeconfig mtime change. `ClientProvider.Do(ctx, fn)` retries `fn` once after a 401, following a rebuild (plan rule 9).
  - Unit tests cover mtime reload and single retry on 401.
- [x] T011 [vmop-TBD] Add the served-version check in `mcp/pkg/kube/discovery.go`.
  - Discovery must report `vmoperator.vmware.com/v1alpha6`, or return an error listing the served versions.
  - Record the served versions.
  - Tests go in `mcp/pkg/kube/kube_test.go`.
- [x] T012 [vmop-TBD] Add the Secret/ConfigMap guard in `mcp/pkg/kube/guard.go`.
  - `GuardedClient` wraps `ctrlclient.Client` and rejects every verb on `Secret`/`ConfigMap` GVKs, typed or unstructured.
  - Export test helper `NewSecretTrap()` returning `interceptor.Funcs`, in `mcp/pkg/kube/trap.go`. It must not import ginkgo: it records violations for tests to assert on.
  - Tests go in `mcp/pkg/kube/kube_test.go`.
- [x] T013 [P] [vmop-TBD] Add the contract types in `mcp/pkg/contract/` (`contract.go`, `cursor.go`, `errors.go`, plus the suite and test files).
  - `Version = "1.0.0"`.
  - Common types and `ToolError` codes per `model.md` §4–§5.
  - Opaque cursor encode/decode with phase.
  - Round-trip tests.
  - `jsonschema.For` must succeed for every exported DTO. Add a test that iterates over them.
- [x] T014 [vmop-TBD] Add the projections in `mcp/pkg/projection/` (`vm.go`, `class.go`, `image.go`, `snapshot.go`, `bootstrap.go`, `inventory.go`, plus the suite and test files).
  - Allow-list projections into the `contract` DTOs.
  - `BootstrapSummary` with `inlineFieldsPresent` paths covering cloud-init `cloudConfig` (`runcmd`, `write_files`, `sshAuthorizedKeys`), `rawCloudConfig` refs, linuxPrep `scriptText`, sysprep (`guiRunOnce`, `scriptText`, `rawSysprep` refs, password refs), and `vAppConfig.properties`.
  - `extraConfig` projected as keys only.
  - Guest-sourced strings wrapped in `Untrusted`, capped at 256 bytes.
  - Field-inventory test (plan rule 2): parse `../../../api/v1alpha6/*.go` with `go/parser` and fail on unclassified fields.
  - Golden tests: for a VM carrying every bootstrap provider with inline secrets, the serialized output contains none of the inline sentinel values.
- [x] T015 [vmop-TBD] Add the toolkit in `mcp/pkg/toolkit/` (`registrar.go`, `namespace.go`, `result.go`, `errors.go`, plus the suite and test files).
  - `Registrar` with `AddRead`, `AddWrite`, and `Gated`, setting all four annotations per `model.md` §3.
  - `NamespacePolicy` per plan rule 6.
  - Result helper: text `Content` + `StructuredContent`, 64 KiB cap, and truncation.
  - `MapError` per `model.md` §5.
  - Tests:
    - annotations for read and write;
    - `AddWrite` omitted when the tier is off (assert via in-memory `ListTools`);
    - namespace refusal makes no API call;
    - cap truncation.
- [x] T016 [vmop-TBD] Add the server assembly in `mcp/pkg/server/` (`server.go`, `testdata/tools_list.golden.json`, plus the suite and test files).
  - `Options{Kubeconfig, Context, Namespace, Namespaces, EnableWrite}`.
  - `New(ctx, Options) (*mcp.Server, error)` builds the provider, checks versions, detects capabilities, and calls every tool package's `Register`.
  - `Implementation{Name:"vmop-mcp", Version: buildinfo.Version}`.
  - Golden test over the full `tools/list`, with an `-update` flag.
  - As each tool lands, its task updates the golden file.
- [x] T017 [vmop-TBD] Add the binary in `mcp/cmd/vmop-mcp/main.go`.
  - Flags: `--kubeconfig`, `--context`, `--namespace`, `--namespaces`, `--enable-write`, `-v`.
  - Route `klog` and `ctrllog` to stderr.
  - `server.Run(ctx, &mcp.StdioTransport{})`.
  - Exit non-zero with a stderr message on startup errors.
- [x] T018 [P] Add the envtest helper in `mcp/test/envtest/envtest.go`.
  - Start `envtest.Environment` with CRDs from `<repo>/config/crd/bases` and `<repo>/config/crd/external-crds/iaas.vmware.com_capabilities.yaml`. Derive `<repo>` from the helper's own source location via `runtime.Caller(0)`, because envtest resolves relative paths against each test package's working directory.
  - Helpers to create a namespaced user via envtest `Environment.AddUser` (present in controller-runtime v0.25.0, `pkg/envtest/server.go:378`), bind it with a `Role`/`RoleBinding`, and write a real kubeconfig file for that user (`AuthenticatedUser.KubeConfig()`). T019, T020, and the reload test in T010 use this file.
  - Do not import ginkgo or gomega.
- [x] T019 [vmop-TBD] Add the stdio hygiene test in `mcp/cmd/vmop-mcp/vmop_mcp_suite_test.go` and `mcp/cmd/vmop-mcp/vmop_mcp_test.go`.
  - `Label(testlabels.MCP, testlabels.EnvTest)`.
  - Build the binary, start it with an envtest kubeconfig via `mcp.CommandTransport`, and assert that `initialize` and `tools/list` succeed and that no write tools are listed.
  - Start it with a kubeconfig that sets `interactiveMode: Always` and assert a non-zero exit.

## Phase 3: US1, inventory and identity (Increment 1)

- [x] T020 [US1] [vmop-TBD] Add the identity tools in `mcp/pkg/tools/identity/` (`identity.go`, plus the suite and test files).
  - `whoami`: `SelfSubjectReview`, with fallback to the kubeconfig user name and `identitySource:"kubeconfig"`. Reports served versions, contract version, tiers, capabilities, and the allow-list.
  - `check_access`: `SelfSubjectAccessReview`.
  - Envtest spec: the impersonated user sees its own name, and a forbidden verb returns `allowed:false`.
- [x] T021 [US1] [vmop-TBD] Add the VM read tools in `mcp/pkg/tools/vm/` (`vm.go`, plus the suite and test files).
  - `list_virtual_machines` and `get_virtual_machine`, per `model.md` §6.
  - Tests:
    - pagination cursor;
    - label selector;
    - forbidden namespace → `forbidden`;
    - allow-list refusal;
    - Secret trap records nothing.
- [x] T022 [P] [US1] [vmop-TBD] Add the VM class read tools in `mcp/pkg/tools/vmclass/` (`vmclass.go`, plus the suite and test files).
  - `list_virtual_machine_classes` and `get_virtual_machine_class`. `configSpec` is never emitted.
- [x] T023 [P] [US1] [vmop-TBD] Add the VM image read tools in `mcp/pkg/tools/vmimage/` (`vmimage.go`, plus the suite and test files).
  - List and get over VMI + CVMI with the merged two-phase cursor and `scope`.
  - A 410 maps to `cursor_expired`.
  - CVMI calls bypass the namespace allow-list.
  - `ovfProperties` are projected as keys only.
- [x] T024 [P] [US1] [vmop-TBD] Add the events tool in `mcp/pkg/tools/events/` (`events.go`, plus the suite and test files).
  - `get_events` filtered by `involvedObject` kind, name, and namespace.
  - Newest first, with the `limit` and `sinceMinutes` caps.
  - Messages returned as `Untrusted`.
- [x] T030 [US1] [vmop-TBD] Add capability detection in `mcp/pkg/capabilities/` (`capabilities.go`, plus the suite and test files).
  - Read the `supervisor-capabilities` `Capabilities` object and map `supports_VM_service_VM_snapshots` → `active` / `inactive`. A forbidden or not-found read maps to `unknown`.
  - **Blocked by** the spec clarifications on DevOps `Capabilities` access and snapshot-kind admission. Implement the proposed behavior and adjust once they are resolved.
- [x] T031 [US1] [vmop-TBD] Add the snapshot read tools in `mcp/pkg/tools/vmsnapshot/` (`vmsnapshot.go`, plus the suite and test files).
  - `list_virtual_machine_snapshots` and `get_virtual_machine_snapshot`, registered via `Registrar.Gated`. They are registered when the capability is `active` or `unknown`, and omitted when it is `inactive`.
  - Tests cover all three states.

## Phase 4: US2, diagnose (Increment 1)

- [x] T040 [US2] [vmop-TBD] Build the diagnose rule engine in `mcp/pkg/diagnose/` (`engine.go`, `rules.go`, plus the suite and test files).
  - Pure functions over a `Snapshot{VM, Class?, Image?, PVCs, Events}` input producing `[]contract.Finding`, per `model.md` §6.
  - Stable rule IDs.
  - Ordering by severity, then priority.
  - Suggestions come only from constants, never from interpolated untrusted text.
- [x] T041 [US2] [vmop-TBD] Add the condition-coverage test in `mcp/pkg/diagnose/diagnose_test.go`.
  - Parse `../../../api/v1alpha6/virtualmachine_types.go` with `go/parser`.
  - Collect every `VirtualMachine…Condition…` and `…Reason…` constant, and fail if any lacks a classification entry in `rules.go`.
- [x] T042 [US2] [vmop-TBD] Add the diagnose tool in `mcp/pkg/tools/diagnose/` (`diagnose.go`, plus the suite and test files).
  - Gather the VM, class, image (VMI, then CVMI), PVCs, and events using the guarded client, then call the engine.
  - A partial-fetch failure (for example a forbidden PVC read) becomes an `info` finding rather than a tool error.
  - Tests cover each acceptance criterion in spec US2, including "never reads the Secret" (via the trap) and "inline content reported by path only".
- [x] T043 [P] [US2] [vmop-TBD] Add the `troubleshoot-vm` prompt in `mcp/pkg/prompts/` (`prompts.go`, plus the suite and test files), per `model.md` §6.

## Phase 5: US3, explain (Increment 1)

- [x] T050 [US3] [vmop-TBD] Add the OpenAPI field lookup in `mcp/pkg/openapi/` (`openapi.go`, plus the suite and test files).
  - Fetch via `k8s.io/client-go/openapi3` `Root.GVSpec` for `vmoperator.vmware.com/v1alpha6` and cache it per process.
  - Resolve a dotted `fieldPath` through `$ref`s, and return type, description, enum, default, required, and direct children.
  - A 403 or 404 maps to `schema_unavailable`.
  - Envtest spec against the real CRD schemas: `spec.powerOffMode` returns the enum `Hard`, `Soft`, `TrySoft`.
- [x] T051 [US3] [vmop-TBD] Add the explain tool in `mcp/pkg/tools/explain/` (`explain.go`, plus the suite and test files).
  - `explain_field`, restricted to VM Service kinds.

## Phase 6: Increment 1 E2E and release gate

- [ ] T060 (code complete; not yet run on a real Supervisor, labeled `experimental`) [US1] [US2] [US3] [vmop-TBD] Add the read-only Supervisor E2E.
  - Files:
    - `test/e2e/vmservice/vmservice/mcp/mcp.go`, exposing `MCPReadSpec(ctx, inputGetter)` in the same style as `vm_webconsolerequest.go`;
    - `test/e2e/vmservice/vmservice_test.go`, adding `Context("MCP-SERVER")` with `Label("mcp")`;
    - `test/e2e/go.mod` and `test/e2e/go.sum`, adding `require` and `replace github.com/vmware-tanzu/vm-operator/mcp => ../../mcp`;
    - `Dockerfile.e2e`, which copies replace targets selectively. Add `COPY mcp/go.mod mcp/go.sum ./mcp/` next to the other manifest copies in `builder-base` (before `go mod download`), and `COPY mcp/ ./mcp/` in `builder-e2e`. Otherwise the CI `e2e-image-build` job fails.
  - Create a DevOps SSO user with edit access via `testutils.CreateUserAndLogin` and `testutils.SetUserPermissionsOnNamespace`.
  - Build the server in-process via `server.New` using that user's kubeconfig, and connect over in-memory transports.
  - Assert:
    - `whoami` shows the SSO user;
    - `list_virtual_machines` includes the suite's Linux VM;
    - `diagnose_virtual_machine` on it is `healthy`;
    - `explain_field(VirtualMachine, spec.powerState)` either succeeds or returns `schema_unavailable`. Record which, to resolve the spec clarification;
    - an allow-list refusal.
  - Clean up the user.
  - Per `e2e-sync-with-changes.md`, this task touches no `pkg/` or `api/` code.
- [x] T061 Update `test/e2e/README.md` with the `mcp` label and how to run the MCP E2E specs.

## Phase 7: US4, write tier (Increment 2, contract 1.1.0)

- [x] T070 [US4] [vmop-TBD] Add the power tools in `mcp/pkg/tools/vmpower/` (`power.go`, `restart.go`, `wait.go`, `write.go`, plus the suite and test files).
  - `set_virtual_machine_power_state` and `restart_virtual_machine` (registered via `AddWrite`), and `wait_for_virtual_machine` (registered via `AddWrite` too; it is annotated read-only but only offered with the write tier, see `model.md` §2), per `model.md` §7.
  - `write.go` holds the shared read → guard → patch → 409-retry helper (plan rule 11).
  - Tests:
    - mode mapping, and `PoweredOn`+mode → `invalid`;
    - group guard and `force`;
    - no-op skip (`changed:false`, no patch sent);
    - 409 on the first attempt succeeds on retry, and 409 three times → `conflict`;
    - `dryRun` sends `DryRunAll`;
    - restart is annotated `idempotentHint:false`;
    - wait timeout and cancellation.
  - Bump `contract.Version` to `1.1.0` in `mcp/pkg/contract/contract.go`.
- [x] T071 [P] [US4] [vmop-TBD] Add the read-tier (`AddRead`) storage discovery tool in `mcp/pkg/tools/storage/` (`storage.go`, plus the suite and test files).
  - `list_storage_classes` from namespace `ResourceQuota` keys `<sc>.storageclass.storage.k8s.io/requests.storage`.
  - **Blocked by** the spec clarification on storage discovery.
- [x] T072 [US4] [vmop-TBD] Add the create tool in `mcp/pkg/tools/vmcreate/` (`create.go`, `preflight.go`, plus the suite and test files).
  - `create_virtual_machine` with the constrained input. `dryRun` is required.
  - Preflight reuses `mcp/pkg/diagnose` lookups (class, image, storage, name collision).
  - Build a `vmopv1.VirtualMachine` from the input, and `Create` with `FieldOwner("vmop-mcp")`, plus `DryRunAll` when requested.
  - Project the **response** object.
  - Tests:
    - missing class → blocking preflight and no API create;
    - valid dry run → nothing persisted in the fake client;
    - bootstrap refs become `SecretKeySelector`s without reading the Secret (trap).
- [x] T073 [P] [US4] [vmop-TBD] Add the `create-vm` prompt in `mcp/pkg/prompts/prompts.go`, with tests in `mcp/pkg/prompts/prompts_test.go`.
- [x] T074 [US4] [vmop-TBD] Add envtest write specs in `mcp/pkg/tools/vmpower/vmpower_test.go`.
  - `Label(testlabels.MCP, testlabels.EnvTest)`.
  - A real optimistic-lock conflict (a concurrent patch between read and write) is retried and never overwrites.
  - A user without `patch` gets `forbidden`.

## Phase 8: Increment 2 E2E

- [ ] T080 (code complete; not yet run on a real Supervisor, labeled `experimental`) [US4] [vmop-TBD] Add the write-tier Supervisor E2E.
  - Files: `test/e2e/vmservice/vmservice/mcp/mcp.go` (add `MCPWriteSpec`) and `test/e2e/vmservice/vmservice_test.go`.
  - As the DevOps user with `EnableWrite`:
    - `create_virtual_machine{dryRun:true}` with a bogus class returns a preflight blocking finding;
    - a valid dry run is admitted, shows the resolved image, and `get` confirms nothing was persisted;
    - a real create, then `wait_for_virtual_machine{powerState:PoweredOn}`;
    - power off `Soft`, then wait;
    - where VM groups are enabled, a group member's power change is refused.
  - Delete the VM with the suite's existing helpers. There is no MCP delete tool.

## Phase Final: Polish

- [x] T090 [vmop-TBD] Add release packaging.
  - A `vmop-mcp-dist` Makefile target that cross-compiles darwin, linux, and windows on amd64 and arm64 into `bin/vmop-mcp-<os>-<arch>[.exe]` with SHA256 sums.
  - Document the `mcp/vX.Y.Z` tag scheme in `mcp/README.md`.
  - **Blocked by** the spec clarification on the distribution channel.
  - File: `Makefile`.
- [x] T091 [vmop-TBD] Write the user guide.
  - `docs/guides/mcp-server/README.md` covers:
    - install;
    - host configuration snippets for Claude Code, Claude Desktop, VS Code, and Cursor;
    - login flow and token expiry;
    - tiers and `--namespaces`;
    - the tool catalog, with the RBAC verbs each tool needs;
    - the privacy guarantees (no Secret reads, the redaction inventory);
    - the compatibility matrix.
  - `mkdocs.yml`: add a nav entry under Guides.
- [ ] T092 [P] Update `.sdd/specs/010-mcp-server/spec.md` and `.sdd/specs/010-mcp-server/research.md`.
  - Resolve every `[NEEDS CLARIFICATION]` with the E2E findings.
  - Flip the status to `Implemented` in the final PR.
  - Update the status in `.sdd/INDEX.md`.

## Coverage check

| Spec goal | Tasks |
|---|---|
| G-1, G-7, G-8 | T010, T020, T021, T060 |
| G-2 | T012, T014, T021, T042, T072 |
| G-3 | T014, T022, T023 |
| G-4, G-5 | T015, T016, T019, T070 |
| G-6 | T015, T021, T023, T060 |
| G-9 | T011 |
| G-10 | T030, T031 |
| G-11 | T013, T016, T070 |
| G-12 | T020 |
| G-13 | T021–T023, T031 |
| G-14 | T024 |
| G-15 | T040–T042 |
| G-16 | T050, T051 |
| G-17 | T013, T015 |
| G-18–G-20, G-22, G-23 | T070, T074 |
| G-21 | T072, T080 |
| G-24 | T071 |
| G-25 | T060, T080 |
