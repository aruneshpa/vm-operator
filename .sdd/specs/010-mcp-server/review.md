# Architecture Review: MCP Server for VM Operator (`proposal.md`)

- **Reviewed**: `.sdd/specs/010-mcp-server/proposal.md` (Draft, 2026-09-30)
- **Review date**: 2026-09-30
- **Scope**: security and auth, feasibility against the real v1alpha6 API and webhooks, module/build/CI, constitution/SDD conformance, scope, output contract.
- **Method**: I checked each factual claim against the repository at `efcdb7de1` and against the MCP Go SDK source (`github.com/modelcontextprotocol/go-sdk@v1.8.0`). I tested schema-inference behavior empirically with `github.com/google/jsonschema-go@v0.4.2`, which was the version in the local module cache. The SDK pins v0.4.3; see R-7.

---

## Verdict: **Approve with changes**

The core direction is sound:

- a client-side stdio binary,
- the user's own kubeconfig identity,
- Kubernetes API only,
- read-only by default,
- tiers that are not registered unless enabled,
- deferral of in-cluster HTTP.

The token-passthrough argument for deferring topology B is correct. There is also good in-repo precedent for a separately-moduled binary (`test/e2e`).

Three things must change before `spec.md`, `plan.md`, and `tasks.md` are written:

- The Secret handling contradicts the proposal's own hard boundary.
- The redaction design is a deny-list over a type that has many inline-payload fields.
- The loopback HTTP transport has no authentication.

Several tool semantics are also wrong against the real webhooks (resize, dry-run coverage, feature-gated kinds). Several build/CI claims are inaccurate. All of these can be fixed in the document without changing the recommended topology.

---

## Blocking

### R-1 Drop the bootstrap Secret `get`. Derive existence from the VM's own conditions.

- **Section**: 4 UC-1; 7.3 hard boundary 3; 9 open question 3.
- **Evidence**:
  - The proposal says both "No Secret or ConfigMap reads" (7.3 #3, 6 Goals) and "`exists` comes from a `get` whose returned body is discarded". A `get` on a Secret *is* a Secret read:
    - The full `data` payload crosses the wire into the process.
    - It is recorded as a secrets read in the audit log.
    - It requires the same `get secrets` RBAC as reading the value.
  - A metadata-only GET (`metav1.PartialObjectMetadata`) is not a safe substitute. It still needs `get secrets`. It also returns `metadata.annotations`, and for any Secret created with `kubectl apply` that includes `kubectl.kubernetes.io/last-applied-configuration`, which contains the Secret's `data`.
  - The information is already available without touching Secrets:
    - The VM controller fetches the bootstrap Secret and records the result on the VM as `VirtualMachineBootstrapReady=False` with the API error's reason and message (`pkg/providers/vsphere/vmprovider_utils.go:339-344`).
    - It records a missing key as `RequiredKeyNotFound` (`pkg/providers/vsphere/vmprovider_utils.go:381`).
    - This is stronger than an "exists" check, because the controller also validates keys.
  - Legacy VMs may source bootstrap data from a **ConfigMap** when the v1alpha1 transport annotation is present (`pkg/providers/vsphere/vmprovider_utils.go:323-336`). An "exists" design would need to special-case this too.
- **Recommendation**:
  - Remove the `exists` probe entirely.
  - Report bootstrap references as `{kind, name, key}`, plus the `VirtualMachineBootstrapReady` condition's reason and message.
  - Resolve open question 3 as "no".
  - Make "the server never issues any request against `secrets` or `configmaps`" a testable acceptance criterion. For example, a fake-client interceptor fails the test on any Secret or ConfigMap GVK.

### R-2 Redaction must be an allow-list projection, not a deny-list. The deny-list as written misses most inline payload fields.

- **Section**: 3 ("never Secret contents"); 7.3 #4; 7.5 (`get` adds "a sanitized spec and status"); 8 (`verbose` flag).
- **Evidence**:
  - 7.3 #4 strips `managedFields` and last-applied, summarizes `spec.bootstrap`, and masks "inline sysprep or vApp property values". The v1alpha6 types carry arbitrary inline, user-authored payloads in many more places:
    - `spec.bootstrap.cloudInit.cloudConfig.runcmd` is `json.RawMessage` (`api/v1alpha6/cloudinit/cloudconfig.go:55`).
    - `write_files[].content` is inline string *or* Secret ref, as `json.RawMessage` (`cloudinit/cloudconfig.go:291`).
    - `sshAuthorizedKeys` (`virtualmachine_bootstrap_types.go:128`, `cloudinit/cloudconfig.go:197`).
  - More inline payload fields:
    - `spec.bootstrap.linuxPrep.scriptText.value` (`virtualmachine_bootstrap_types.go:211`, with `ValueOrSecretKeySelector.Value` at `common/types.go:68-71`).
    - `spec.bootstrap.sysprep.sysprep.guiRunOnce.commands` (`sysprep/sysprep.go:70`) and `scriptText` (`sysprep/sysprep.go:61`).
    - `spec.bootstrap.vAppConfig.properties[].value.value`, which is inline and commonly holds passwords or tokens for OVF appliances (`virtualmachine_bootstrap_types.go:281`, `common/types.go:89-97`).
    - `spec.advanced.extraConfig`, which is arbitrary VMX key/value pairs (`virtualmachine_types.go:1357`).
    - `status.extraConfig`, which reflects effective VMX keys merged from image, class, and spec (`virtualmachine_types.go:1563`; populated at `pkg/providers/vsphere/vmlifecycle/update_status.go:2249-2288`).
  - **`VirtualMachineReplicaSet.spec.template.spec` is a full `VirtualMachineSpec`** (`virtualmachinereplicaset_types.go:72,107`). Every bootstrap field above reappears there. The proposal's redaction discussion covers only VMs.
  - `VirtualMachineClass.spec.configSpec` is raw vSphere ConfigSpec JSON and can include `extraConfig` (for example `guestinfo.*`).
  - A deny-list will silently regress every time a field is added to the API. The v1alpha6 VM schema alone spans `config/crd/bases/vmoperator.vmware.com_virtualmachines.yaml:13513-18517`.
- **Recommendation**:
  - Every tool output is a **projection into a curated DTO** (see R-7). Fields are copied in explicitly; nothing is copied by default.
  - Bootstrap is always represented as `{provider, secretRefs[], inlineFieldsPresent: [paths]}`. Never include inline values.
  - `extraConfig` (spec and status) is reported as keys only. Values appear only for an explicit allow-list of known-safe keys.
  - Drop the `verbose` "full sanitized object" mode. If it stays, it must go through the same allow-list with a documented field inventory.
  - Add a regression test that walks the reflected `vmopv1` types. It should fail when a new `string`, `json.RawMessage`, `[]string`, or `ValueOrSecretKeySelector` field appears under `spec.bootstrap`, `spec.advanced`, or any `template` without an explicit classification.

### R-3 Loopback Streamable HTTP has no authentication. Cut it from v1, or specify authn and Origin checks.

- **Section**: 6 Goals ("Optional loopback-only Streamable HTTP"); 7.9 Phase 2.
- **Evidence**:
  - Binding to `127.0.0.1` restricts the network but not the principal. Any local process, and any other user on a shared jump host, can connect and drive the tools with the invoking user's Supervisor credentials, including write and destructive tiers if enabled. That is a confused-deputy on the workstation.
  - The SDK's default protections are narrower than "loopback-only" implies:
    - DNS-rebinding protection is on by default. It only rejects requests that arrive on a loopback address with a non-localhost `Host` header (`mcp/streamable.go:174-182`, enforced at `:319-327`).
    - Cross-origin protection is **off by default**. The `CrossOriginProtection` option is nil unless set, and is deprecated in favor of wrapping the handler with `http.NewCrossOriginProtection()` (`mcp/streamable.go:184-195`, `:328-333`).
    - Nothing authenticates the caller.
- **Recommendation**:
  - Remove HTTP from v1 goals. All mainstream hosts support stdio.
  - If a concrete host requires HTTP, specify all of the following as acceptance criteria:
    - a per-launch random bearer token, printed once to stderr or written to a 0600 file, and required on every request;
    - bind to `127.0.0.1`/`::1` only, and reject `0.0.0.0`;
    - wrap with `http.NewCrossOriginProtection()`;
    - keep `DisableLocalhostProtection=false`;
    - `MaxRequestBodyBytes` left at or below the default.
  - Also consider a Unix domain socket with 0600 permissions instead of TCP.

---

## Major

### R-4 `resize_virtual_machine` as described fails whenever `spec.class` is set, and is feature-gated.

- **Section**: 4 UC-4 ("resize (`spec.className`)"); 7.5 write tier.
- **Evidence**:
  - Changing `spec.className` is rejected as immutable unless the `VMResizeCPUMemory` FSS is on (`webhooks/virtualmachine/validation/virtualmachine_validator.go:729-731`, flag at `pkg/config/config.go:196`).
  - With `ImmutableClasses` on (`pkg/config/config.go:205`), the mutating webhook passes the object through unchanged when both `spec.className` and `spec.class` are set (`webhooks/virtualmachine/mutation/virtualmachine_mutator.go:182-186`).
  - The validator then rejects the request because the existing `spec.class` instance is not owned by the new class (`virtualmachine_validator.go:709-719`).
  - So a patch that only changes `className` on a VM already carrying `spec.class` is denied.
  - The mutator only picks the active instance when `spec.class` is nil (`virtualmachine_mutator.go:188-217`).
  - Resizes may also need a power cycle. The `PowerOffRequired` and `PowerCyclePending` reasons exist for this (`api/v1alpha6/virtualmachine_types.go:186-194`).
- **Recommendation**:
  - The resize patch must set `spec.class` to `null` alongside the new `spec.className`, or set it to a specific active `VirtualMachineClassInstance`.
  - The spec should state that resize requires server-side support. Detect this via dry-run, and surface the immutable-field error as "resize not enabled on this Supervisor".
  - After resize, report the resulting pending-power-cycle condition.

### R-5 Server-side dry-run does not validate what the proposal says it validates.

- **Section**: 3 ("Server-side dry-run surfaces webhook validation errors"); 5 S-5; 7.4; 7.7 integration.
- **Evidence**:
  - S-5 is wrong. VirtualMachineClass has been namespaced since v1alpha2 (`api/v1alpha6/virtualmachineclass_types.go:177`). "Bound to the namespace" is the v1alpha1 `VirtualMachineClassBinding` concept (`api/v1alpha1/condition_consts.go:20-22`).
  - More importantly, the VM admission webhooks never check that the class exists:
    - `validateClassOnCreate` only checks the classless case and, under `ImmutableClasses`, the instance (`virtualmachine_validator.go:652-675`).
    - There is no class `Get` in `webhooks/virtualmachine/`.
    - A VM with a bad class is admitted, and dry-run succeeds. The failure shows up later as `VirtualMachineClassReady=False`.
  - Likewise, placement, storage-quota, and network-provider failures are reconcile-time errors, not admission errors.
  - Image name resolution *is* admission-time (`api/v1alpha6/virtualmachine_types.go:846-853`).
  - Because the MCP module cannot import the root module (7.6), envtest in `mcp/` runs **without** VM Operator's webhooks. The 7.7 claim that envtest "exercises ... server-side dry-run" can therefore only exercise CRD schema validation, not VMOP admission.
- **Recommendation**:
  1. `create_virtual_machine` runs **preflight checks** before the dry-run, reusing the diagnose rules: class exists in the namespace, image resolves, storage class is in the namespace's quota, and referenced PVCs exist.
  2. Use the **dry-run response body**. It is the object after mutating webhooks. Show the resolved `spec.image`, `spec.class`, and defaulted fields from it instead of re-implementing webhook resolution logic in the sub-module.
  3. Rewrite S-5 around a real admission failure, for example image display-name ambiguity or `nextRestartTime` on create (`virtualmachine_validator.go:2220-2232`).
  4. Move the read-only Supervisor E2E from Phase 3 to Phase 1, and add a dry-run create case. That is the only tier where VMOP admission is real.

### R-6 Feature-gated kinds need capability-aware tool registration.

- **Section**: 6 Goals; 7.4 rules; 7.5 catalog.
- **Evidence**:
  - The controllers for replica sets (`K8sWorkloadMgmtAPI`), snapshots (`VMSnapshots`), and groups and group publish (`VMGroups`) are registered only when their FSS is enabled (`controllers/controllers.go:63-87`).
  - Snapshot webhooks are gated the same way (`webhooks/webhooks.go:78`).
  - The CRDs, however, ship in `config/crd/bases` regardless. So on a Supervisor with a feature off, the kind may still be discoverable. `create_virtual_machine_snapshot` or `revert_virtual_machine_snapshot` could then write objects that nothing reconciles, or fail admission in unclear ways.
  - Snapshot revert via `spec.currentSnapshotName` (`virtualmachine_types.go:1172-1194`) depends on the same gate.
- **Recommendation**:
  - At startup, determine which capabilities are active and register only the tools whose backing feature is on. `tools/list` then reflects what the Supervisor supports, the same way it reflects tiers.
  - Define the capability source in the spec. Options include the cluster-scoped `Capabilities` resource (`external/capabilities/api/v1alpha1/capabilities_types.go:139-152`) or an agreed signal. See the [NEEDS CLARIFICATION] items at the end of this review.
  - The SDK supports `RemoveTools` and emits `tools/list_changed` (`mcp/server.go:613-616`) if capabilities are re-evaluated later.

### R-7 Tool input and output types must be curated DTOs. Raw `vmopv1` types break SDK schema inference.

- **Section**: 3 ("Tool inputs are typed against v1alpha6, so the model cannot invent fields"); 7.5; 8 (golden tests).
- **Evidence**:
  - `mcp.AddTool[In, Out]` infers input and output schemas from the Go types. It **panics** if inference fails (`mcp/server.go:603-609`). It then validates every call's arguments against the inferred schema (`mcp/server.go:400-406`).
  - I tested inference empirically with `jsonschema-go` v0.4.2. The SDK v1.8.0 pins v0.4.3 (`go.mod`), so this should be re-confirmed on v0.4.3, but the behavior follows from type reflection:
    - `jsonschema.For[metav1.Condition]` fails with `custom schema for embedded struct must have type "object", got "string"`, because `metav1.Time` embeds `time.Time`.
    - `jsonschema.For[metav1.ObjectMeta]` fails the same way.
    - `jsonschema.For[vmopv1.VirtualMachineStatus]` fails the same way.
    - Any `Out` type that contains conditions or object metadata would therefore panic at registration.
  - `jsonschema.For[vmopv1.VirtualMachineSpec]` succeeds but is wrong:
    - `resource.Quantity` becomes `{"type":"object","properties":{},"additionalProperties":false}`. This happens, for example, at `spec.volumes[].persistentVolumeClaim.instanceVolumeClaim.size`, so a correct input like `"10Gi"` would be rejected.
    - `json.RawMessage` (`runcmd`, `write_files.content`) and `intstr.IntOrString` have similar problems.
    - Every non-`omitempty` field becomes required.
- **Recommendation**:
  - Every tool's `In` and `Out` is a small, purpose-built struct in `mcp/pkg/tools` (or a `contract` package). Use `jsonschema` struct tags for descriptions and enums, and plain `string` for quantities and times.
  - `create_virtual_machine` accepts a **constrained** input: name, class, image, storage class, optional network interface names, optional bootstrap Secret refs, power state, labels. It does not accept a full `VirtualMachineSpec`.
  - Anything more advanced is out of scope for v1, or goes through a separately-reviewed `apply_manifest{dryRun}` tool. That tool should be labeled as not "correct by construction".
  - Replace the "cannot invent fields" benefit with "inputs are validated against a curated schema, and the API server's OpenAPI and admission validation are authoritative".

### R-8 Build and CI claims are wrong or incomplete.

- **Section**: 7.6; 8 ("`vulncheck-go` covers the module").
- **Evidence**:
  - **vulncheck**: `vulncheck-go` runs `$(GOVULNCHECK) ./...` from the repo root (`Makefile:931-932`). The CI job does the same (`.github/workflows/ci.yml:208-229`). That scans only the root module. It does **not** cover `api/`, `test/e2e/`, or a new `mcp/` module. The mitigation in section 8 is currently false.
  - **Tests in CI**: CI does not use the default `COVERED_PKGS`. Each matrix entry sets it explicitly (`.github/workflows/ci.yml:256-317`). A new `mcp/` module gets **no CI test run** unless a matrix row is added, for example `name: mcp, covered-pkgs: ./mcp, make-target: test-nocover`.
  - Nested modules work under the existing runner, because ginkgo compiles each suite in its own directory (`ginkgo/v2@v2.32.1/ginkgo/internal/compile.go:56-57`). That is how the `api` row runs `./api/...`, which includes the separate `api/test` module.
  - The local default `COVERED_PKGS` (`Makefile:127-129`) would include `./mcp`. Whether to exclude it is a local-UX choice, not a correctness requirement. The same is already true of `./test` → `test/e2e`.
  - **lint/modules**:
    - `lint-go` picks up `./mcp/` via `GO_MOD_DIRS_TO_LINT` (`Makefile:57-58, 318-323`). That part of the claim is correct.
    - `modules` (`go mod tidy` per module, `Makefile:374-383`) and CI's `git diff --exit-code` (`ci.yml:78-81`) will also cover it.
    - Dependabot lists module directories explicitly (`.github/dependabot.yaml:6-9`: `/`, `/hack/tools`, `/test/e2e`). `/mcp` must be added, or the SDK and k8s dependencies will go stale.
  - **`go install` distribution**:
    - `go install .../mcp/cmd/vmop-mcp@vX` refuses modules whose `go.mod` contains `replace` directives. The proposed `replace => ../api` rules that channel out.
    - Sub-module releases also need their own tag prefix (`mcp/vX.Y.Z`). The repo already does this for `api/` (tags `api/v1.9.0`, etc.).
  - **Prior art**:
    - `cmd/web-console-validator` is in the **root** module. It imports root `pkg/` (`cmd/web-console-validator/main.go:23-24`), uses in-cluster config (`:72`), and ships in the operator image (`Makefile:889`). It is precedent for the "thin main + logic package" shape only.
    - The correct precedent for a separately-moduled consumer with `replace` directives, its own CI job, and its own Dependabot entry is `test/e2e` (`test/e2e/go.mod:5-24`; `.github/workflows/ci.yml:237-241`).
  - **Version skew**: `api/go.mod` declares `go 1.23.0` and `controller-runtime v0.19.0`, while root uses `v0.25.0` / `client-go v0.37.0`. The MCP module will select its own k8s versions under MVS. That is fine, but it is a fourth k8s dependency set to keep in lock-step.
- **Recommendation**:
  - Change `vulncheck-go` to iterate modules. For example, loop over `GO_MOD_DIRS` excluding `external/` and `hack/tools/`, or add a dedicated `vulncheck-mcp` target and CI step.
  - Add an `mcp` CI test matrix row and a build row (cross-compile smoke).
  - Add `/mcp` to Dependabot.
  - Decide the distribution channel now (see the decisions list). If `go install` is desired, the MCP module must `require` a tagged `api/vX` instead of `replace`. Otherwise state "binaries only".
  - Cite `test/e2e` as the module precedent.

### R-9 Write tools ignore ownership and group-level power control.

- **Section**: 4 UC-4 and UC-5; 7.4; 7.5 write and destructive tiers.
- **Evidence**:
  - **Groups**: `VirtualMachineGroup.spec.powerState`, `powerOffMode`, `suspendMode`, and `nextForcePowerStateSyncTime` drive member VMs' power state (`api/v1alpha6/virtualmachinegroup_types.go:105-150`). Changing a member VM's `spec.powerState` directly may be reconciled back on the next group sync.
  - **Replica sets**: VMs owned by a `VirtualMachineReplicaSet` are recreated from `spec.template` (`virtualmachinereplicaset_types.go:72-107`). Deleting one is a no-op in effect, and resizing one drifts from the template.
  - **Power modes**: `set_virtual_machine_power_state` lists only `powerOffMode`. Suspend uses `spec.suspendMode` (`virtualmachine_types.go:989-1000`) and restart uses `spec.restartMode` (`:1021-1032`). `Suspended` is not allowed on create (`:964-967`).
  - **Idempotency**:
    - Restart is **not** idempotent. Each `nextRestartTime: "now"` is converted by the mutator into a new timestamp and triggers a new restart (`virtualmachine_mutator.go:443-466`, `virtualmachine_types.go:1004-1016`).
    - Setting a power state is idempotent.
  - **Scale**: the RS has a scale subresource (`virtualmachinereplicaset_types.go:156`).
- **Recommendation**:
  - Every write or destructive tool first reads the target.
  - It refuses, or requires `force: true` with an explanation, when the VM has a `spec.groupName` (for power operations) or a controller owner reference to a `VirtualMachineReplicaSet` (for delete and resize), and points to the owning object instead.
  - The power tool takes `{state, mode}` and maps `mode` to `powerOffMode`, `suspendMode`, or `restartMode` by target state.
  - Mark `restart_virtual_machine` with `idempotentHint: false`.
  - Scale via the `/scale` subresource. The RBAC is `virtualmachinereplicasets/scale`.
  - Mutations using `MergeFromWithOptimisticLock` should do a bounded re-read-and-retry on 409, re-checking preconditions. The VM controller writes to VM spec (for example schema-upgrade backfill), so conflicts will happen.

### R-10 The OpenAPI resource is too large to be useful as specified.

- **Section**: 4 UC-7; 7.5 resources; 9 open question 4.
- **Evidence**:
  - The v1alpha6 `VirtualMachine` schema alone is about 5,000 lines of YAML in the CRD (`config/crd/bases/vmoperator.vmware.com_virtualmachines.yaml:13513-18517`). The CRD file is about 1 MB. The replica set CRD is about 700 KB, because it embeds the VM spec.
  - `/openapi/v3/apis/vmoperator.vmware.com/v1alpha6` returns every kind in the group-version in one document.
  - Returning `vmop://openapi/v1alpha6/VirtualMachine` whole would consume a large share of a model's context for a one-field question.
  - The access assumption is reasonable: `system:discovery` grants `/openapi/*` to authenticated users in upstream Kubernetes. It still needs Supervisor verification, as the proposal says.
- **Recommendation**:
  - Replace the per-kind resource with an `explain` tool or resource template scoped by field path, for example `vmop://explain/v1alpha6/VirtualMachine/spec.powerOffMode`. It should return only that node's description, type, enum, default, and direct children, similar to `kubectl explain`.
  - Fetch the group-version document once per session with ETag and cache it.
  - If `/openapi/v3` is forbidden, fall back to a copy embedded from `config/crd/bases` at build time, and mark the result as potentially skewed.

### R-11 Handle API version skew between the binary and the Supervisor.

- **Section**: 6 Non-goals ("Only ... v1alpha6"); 8 (tool-contract drift).
- **Evidence**:
  - The binary compiles in `vmopv1` = v1alpha6, the storage version in this tree (`config/crd/bases/vmoperator.vmware.com_virtualmachines.yaml:18517-18518`).
  - Supervisors in the field run older VM Operator releases, which may not serve v1alpha6. A newer Supervisor may serve fields this binary does not know. The typed decode silently drops them, and a DTO projection would omit them.
- **Recommendation**:
  - At startup, use discovery to confirm `vmoperator.vmware.com/v1alpha6` is served. Otherwise fail fast with a clear message, and report the served versions in `whoami`.
  - Document the support matrix: which VMOP releases each `vmop-mcp` release supports.
  - Compute patches as a diff between two typed objects (as the proposal says), never as a full `Update`, so that unknown newer fields are never nulled. Add a test for this.

### R-12 Host approval is not a security control. Add server-side confirmation for destructive tools.

- **Section**: 7.4 ("Hosts are expected to prompt... The server's annotations drive that UX"); 8 prompt-injection row.
- **Evidence**:
  - The SDK's own doc on `ToolAnnotations` says: "Clients should never make tool use decisions based on ToolAnnotations received from untrusted servers" (`mcp/protocol.go:1960-1963`).
  - Hosts commonly offer "always allow" or auto-approve modes.
  - Once `--enable-destructive` is set, a prompt injection can call `delete_virtual_machine` directly. Injection sources include:
    - VM annotations and labels;
    - event messages;
    - **guest-reported status**, such as `status.network.*.hostName` and guest IPs/DNS from VMware Tools (`api/v1alpha6/virtualmachine_network_types.go:318,428,464`), which is controlled by whoever controls the guest OS;
    - image and OVF product text authored by third parties.
- **Recommendation**:
  - Destructive tools require a second argument that proves intent. Either:
    - `confirm: "<namespace>/<name>"` must match the target exactly, or
    - a two-step flow: `plan_*` returns an opaque, short-lived token bound to `{op, target, resourceVersion}`, and `execute` requires that token.
  - If the client advertises elicitation support, use it to get a confirmation that the host shows directly to the user.
  - Per resource, mark guest-, image-, and user-sourced strings as untrusted in output. Cap their length, and never interpolate them into `suggestion` text.
  - Consider a `--namespaces` allow-list as a *required* companion to `--enable-destructive`.

---

## Minor

### R-13 Set SDK hint pointers explicitly. The defaults are the unsafe values.

- **Section**: 7.4 tier table.
- **Evidence**: In `ToolAnnotations`, a nil `DestructiveHint` means **true** and a nil `OpenWorldHint` means **true** (`mcp/protocol.go:1964-1991`).
- **Recommendation**:
  - Always set both pointers on every tool. Leaving them nil would advertise write tools as destructive and read tools as open-world.
  - Add a test that asserts the annotations of every registered tool against a table.

### R-14 Avoid double-encoding output, and define size limits.

- **Section**: 7.5; 8 (large outputs).
- **Evidence**: When `Content` is unset, `ToolHandlerFor` fills it with the JSON of `Out` *in addition to* `StructuredContent` (`mcp/tool.go:46-48`). That doubles tokens on every call for hosts that forward both.
- **Recommendation**:
  - Set `Content` to a compact human- and model-oriented text summary, and keep the full DTO in `StructuredContent`.
  - Specify a hard per-response byte cap (for example 64 KiB) with an explicit `truncated: true` and a `continue` hint.
  - Cap events by count and age.

### R-15 Pagination semantics need definition.

- **Section**: 7.5 ("List tools accept `labelSelector`, `limit`, and `continue`"); `list_virtual_machine_images` ("Namespace and cluster images").
- **Evidence**:
  - Kubernetes `continue` tokens are server-issued and expire (410 Gone after compaction).
  - A merged list of namespaced `VirtualMachineImage` and cluster-scoped `ClusterVirtualMachineImage` (`virtualmachineimage_types.go:307,345`) cannot be paged with a single upstream token.
- **Recommendation**:
  - Make the `continue` value an opaque MCP-level cursor that encodes `{kind, upstreamToken}`.
  - Page namespaced images, then cluster images.
  - Map 410 to a clear "restart listing" error.
  - Document that list results are not a consistent snapshot.

### R-16 Diagnose should cover the conditions that actually explain stuck VMs.

- **Section**: 2.2; 4 UC-1; 7.5 `vmop://docs/conditions`.
- **Evidence**:
  - Beyond the conditions listed in the proposal, the API defines several that often explain a stuck VM:
    - `GuestCustomization` with reasons `Pending`, `Running`, and `Failed`;
    - `VirtualMachineTools`;
    - `VirtualMachineReconcilePaused`;
    - `PowerOffRequired` and `PowerCyclePending`;
    - snapshot-revert reasons;
    - `VirtualMachineConditionVMSetResourcePolicyReady`;
    - `VirtualMachineConditionImageCacheReady`.
  - These are at `api/v1alpha6/virtualmachine_types.go:21-292`.
  - A hand-maintained `docs/conditions` page will drift.
- **Recommendation**:
  - Generate the condition reference from the `api/v1alpha6` constants and their doc comments at build time, for example with `go generate` in the MCP module. Add a test that fails when a new `...Condition*` or `...Reason` constant has no diagnose rule classification.

### R-17 The stdio transport imposes process hygiene requirements.

- **Section**: 7.6 `cmd/vmop-mcp`; 5 S-4.
- **Evidence**:
  - With stdio, stdout *is* the JSON-RPC channel.
  - Any library write to stdout corrupts the session: klog or controller-runtime misconfiguration, client-go warning handlers, or exec credential plugins.
  - A kubeconfig `exec` plugin with `interactiveMode: Always` will try to use stdin, which is also the protocol channel.
  - Kubeconfig reload on 401 is feasible. Rebuild the `rest.Config` and client from `clientcmd` when the kubeconfig file's mtime changes, or on a 401. Per-request re-reads are wasteful. `exec`-based credentials refresh automatically in client-go. Avoid a discovery-backed RESTMapper rebuild per request by using a static mapper for the known `vmopv1` kinds.
- **Recommendation**:
  - Route all logging to stderr.
  - Set a client-go warning handler that logs to stderr.
  - Reject, with a clear error, kubeconfigs whose exec plugin demands interactive mode.
  - Specify reload as "on file change or on 401, at most once per request".

### R-18 depguard and import constraints in the sub-module.

- **Section**: 7.6.
- **Evidence**:
  - `.golangci.yml:240-241` denies `k8s.io/utils` and points to root `pkg/util/ptr`, which the MCP module cannot import.
  - The dot-import allow-list and other path-based exclusions in `.golangci.yml` were written for root paths.
- **Recommendation**:
  - Use the Go 1.26 `new(expr)` form or a tiny local `ptr` helper.
  - Confirm that `golangci-lint` run from `mcp/` resolves the root `.golangci.yml` and its path exclusions as intended.
  - Add the new `mcp/...` import aliases (if any) to `importas`.

### R-19 Publish request and storage discovery need extra APIs that the proposal doesn't list.

- **Section**: 4 UC-3, UC-4; 7.5.
- **Evidence**:
  - `VirtualMachinePublishRequest.spec.target.location` defaults to `imageregistry.vmware.com/v1alpha1` `ContentLibrary` (`virtualmachinepublishrequest_types.go:190-212`). Choosing a target requires reading that external API. The proposal says the module imports "only the `api` module".
  - UC-3 step 1 ("lists ... storage classes") has no tool in the catalog. DevOps users typically see storage through namespace quota, not cluster-scoped `StorageClass` list.
- **Recommendation**:
  - Either cut publish from v1 (see R-22), or add `list_content_libraries` using unstructured or the external module.
  - Add `list_storage_classes`, backed by the namespace's `ResourceQuota` / `StoragePolicyQuota`, or clarify how storage is discovered.

### R-20 Correct S-1 and add an E2E module note.

- **Section**: 5 S-1; 7.7 E2E.
- **Evidence**:
  - S-1 says to change `spec.imageName` because `spec.image` is immutable "in most flows". In fact **both** `spec.image` and `spec.imageName` are immutable on update. The only exception is failover of an imageless VM with the `VMIncrementalRestore` FSS by a privileged account (`webhooks/virtualmachine/validation/virtualmachine_validator.go:267-293`).
  - For E2E: `test/e2e` is its own module and already imports the root module (`test/e2e/go.mod:1-24`). Driving `vmop-mcp` in-process or over `CommandTransport` adds the MCP SDK and the `mcp` module to the E2E module's dependency graph.
- **Recommendation**:
  - Fix S-1 so that recreate is the only path.
  - In `plan.md`, state that the E2E module will `require` and `replace` `../../mcp`, and add an E2E label so these specs can be selected independently.

---

## Nit

### R-21 Constitution and SDD bookkeeping.

- **Section**: 7.8; header.
- **Evidence**:
  - The constitution's sub-module table already omits several existing modules: `test/e2e`, `api-docs`, `external/vim/api`, `external/image-registry-operator`, `external/mobility-operator` (compare `.sdd/memory/constitution.md` "Sub-modules" with `find . -name go.mod`).
  - `AGENTS.md` tells reviewers to flag, at high priority, "Code implementing an in-progress spec that is not guarded by a feature flag".
- **Recommendation**:
  - When adding the `mcp/` row, also backfill the missing rows. Call it out in the PR as a documentation-only amendment.
  - Record the feature-flag exemption in `plan.md` Complexity tracking with the rationale "out-of-manager client binary; tier flags are the gate". Also add a sentence to `AGENTS.md` or the plan's review notes, so AI reviewers don't flag every MCP PR.
  - Fold `proposal.md` into `research.md` when the spec is created. `proposal.md` is not a defined SDD artifact.

### R-22 Scope: v1 is over-scoped. Trim it to what proves value and de-risks unknowns.

- **Section**: 6; 7.5; 7.9.
- **Evidence**:
  - The catalog has 11+ read tool families (about 20 tools), 8 write tools, 3 destructive tools, 2 resources, 2 prompts, and a second transport.
  - Large flat tool lists degrade model tool selection.
  - The main unknowns sit on the Supervisor and would not surface until Phase 3: DevOps RBAC, `/openapi/v3` access, and capability detection.
- **Recommendation**:
  - v1 = `whoami`, `check_access`, VM, class, image, and snapshot read tools, `get_events`, `diagnose_virtual_machine`, `explain` (R-10), and a read-only Supervisor E2E.
  - v1.1 = power, restart, and create with preflight and dry-run (R-5, R-9).
  - Later = resize (R-4), snapshot create and revert, replica set scale, VM service create, publish, groups, support bundle (fold into `diagnose_virtual_machine{include: [...]}` rather than a separate tool), and loopback HTTP (R-3).
  - Consider collapsing secondary kinds (groups, publish requests, services) into one `get_vm_operator_object{kind, name}` with a per-kind DTO, to keep the tool count small.
  - Consider adding `wait_for_virtual_machine{condition|powerState, timeout}`. After any write the model otherwise polls with repeated `get` calls.

### R-23 The output contract needs a versioning policy.

- **Section**: 7.8 ("tool names stable"); 8 (golden tests).
- **Evidence**: MCP has no per-tool versioning. `Implementation.Version` = build version says nothing about compatibility.
- **Recommendation**:
  - Define a contract semver. Additive DTO fields are minor. Renames, removals, or semantic changes are major, and ship as a new tool name with the old one deprecated for N releases.
  - Add a golden test over the full `tools/list` output (names, descriptions, input and output schemas, annotations) so contract changes are visible in review.
  - Expose the contract version in `whoami` and in `Implementation`.

### R-24 Small factual items.

- Go SDK "requires Go ≥ 1.25" is correct (`go-sdk@v1.8.0/go.mod`: `go 1.25.0`). The listed transitive dependencies (`golang-jwt/jwt/v5`, `golang.org/x/oauth2`, `google/jsonschema-go`, `segmentio/encoding`) are correct. `yosida95/uritemplate` and `golang.org/x/time` are also direct dependencies.
- The condition type names in section 2.2 match `api/v1alpha6/virtualmachine_types.go:21-61`.
- `SelfSubjectReview` (`authentication.k8s.io/v1`) is GA since Kubernetes 1.28. Specify a fallback for `whoami` (kubeconfig user plus `SelfSubjectRulesReview`) if an older Supervisor rejects it.
- `--namespaces` must not block the cluster-scoped `ClusterVirtualMachineImage` lookups used by image resolution.
- A new `testlabels.MCP` constant belongs in `pkg/constants/testlabels/test_labels.go`, which is its own module (`go 1.13`) and importable via `replace`. Per testing standards, unit specs carry only the category label and envtest specs add `testlabels.EnvTest`.

---

## Decisions to lock in before writing spec, plan, and tasks

1. **No Secret or ConfigMap requests, ever.** Bootstrap diagnosis comes from VM conditions (R-1).
2. **Allow-list DTO projection** for all outputs. No `verbose` raw mode in v1. There is a field-inventory test (R-2, R-7).
3. **stdio only in v1.** HTTP deferred, or specified with a per-launch token and Origin protection (R-3).
4. **Curated `In`/`Out` structs** for every tool, and a constrained `create_virtual_machine` input (R-7).
5. **Capability-aware registration.** Choose and document the capability source for the snapshots, groups, replica set, and resize features (R-4, R-6).
6. **Create flow = preflight + dry-run + show the mutated object.** Dry-run is not described as full validation (R-5).
7. **Ownership-aware writes** (group or RS owner detection) and **server-side confirmation** for destructive tools (R-9, R-12).
8. **Distribution channel**: binaries only (keep `replace`) or `go install` (require a tagged `api/vX`). Add a `mcp/vX.Y.Z` tag scheme and a VMOP compatibility matrix (R-8, R-11).
9. **CI wiring in the same PR as the module scaffold**: test matrix row, build row, multi-module `vulncheck-go`, Dependabot `/mcp` (R-8).
10. **Read-only Supervisor E2E in Phase 1**, including a dry-run create case (R-5, R-22).
11. **Trimmed v1 scope** per R-22, and a contract-versioning policy with a `tools/list` golden test (R-23).

### Items to convert into `[NEEDS CLARIFICATION]` in `spec.md`

- Can the DevOps role read the cluster-scoped `Capabilities` resource? If not, what signal should the client use to detect FSS-gated features (R-6)?
- When the snapshot, group, or replica set FSS is off, what does admission do for those kinds on a real Supervisor: are the CRDs served, and do webhooks fail closed (R-6)?
- Does the DevOps role grant `get` on `/openapi/v3/*` on a real Supervisor (already open; R-10)?
- Which VMOP releases (served API versions) must `vmop-mcp` v1 support (R-11)?
