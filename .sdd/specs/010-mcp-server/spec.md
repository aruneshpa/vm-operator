# Feature Specification: MCP Server for VM Operator

- **Feature branch**: [`feature/add-mcp-server-for-vmop`](https://github.com/aruneshpa/vm-operator/tree/feature/add-mcp-server-for-vmop)
  - **Fork**: `aruneshpa/vm-operator`
  - **PR target**: `vmware-tanzu/vm-operator`
- **Created**: 2026-09-30
- **Status**: In Progress
- **Epic**: TBD
- **Design record**: [`proposal.md`](./proposal.md) (pre-spec proposal), [`review.md`](./review.md) (architecture review, verdict "Approve with changes"), [`research.md`](./research.md) (evidence and resolved decisions)

---

## Summary

AI assistants (Claude Code, Claude Desktop, VS Code, Cursor, and others) can call external tools through the **Model Context Protocol (MCP)**. This feature delivers an MCP server for VM Service. It is a program that a user runs on their own workstation. It lets an AI assistant inspect, diagnose, explain, and, when the user explicitly allows it, operate VM Service resources on a vSphere Supervisor. The assistant acts **as the user**: it uses the user's existing Supervisor login and is bound by exactly the same permissions.

Nothing is deployed to the Supervisor. VM Operator's controllers, webhooks, APIs, and behavior on the cluster do not change.

The feature ships in two increments within this spec:

- **Increment 1 (read-only)**: identity and access checks; inventory of VMs, VM classes, VM images, and VM snapshots; object events; a one-call VM diagnosis; field-level API explanation.
- **Increment 2 (write, opt-in)**: VM power state changes, VM restart, waiting for a VM to reach a state, and guided VM creation with pre-checks and a server-side dry run.

---

## Goals

### Identity, safety, and privacy

- **G-1** The server MUST act only with the invoking user's own Supervisor credentials, taken from the user's existing login. It MUST NOT hold, request, or fall back to any other identity.
- **G-2** The server MUST NOT issue any request against Kubernetes `Secret` or `ConfigMap` resources, including list, get, watch, or metadata-only reads.
- **G-3** Every value the server returns MUST come from an explicit, documented list of permitted fields. User-authored inline payloads must never be returned. Examples: cloud-init content, script text, vApp property values, and extra-config values. Where such payloads exist, the server MAY report *that* they are present and at which field path.
- **G-4** The server MUST start in read-only mode. Write operations MUST be offered only after an explicit opt-in at launch. When the opt-in is absent, write operations MUST NOT appear in the server's advertised capability list at all.
- **G-5** Each operation MUST correctly declare to the AI assistant whether it is read-only, whether it is destructive, and whether it is idempotent.
- **G-6** The user MAY restrict the server to a set of namespaces at launch. Requests for other namespaces MUST be refused before any call reaches the Supervisor. This restriction MUST NOT block lookups of cluster-scoped VM images.
- **G-7** When the Supervisor denies a request (not authorized, forbidden, not found), the server MUST return the Supervisor's message as an error result. It MUST NOT retry with other credentials.
- **G-8** When the user's login expires and the user logs in again, the server MUST pick up the new credentials without being restarted.

### Compatibility and discoverability

- **G-9** At startup, the server MUST verify that the Supervisor serves the VM Service API version it was built for. If it does not, the server MUST exit with a clear message that names the versions the Supervisor does serve.
- **G-10** The server MUST advertise operations for a feature-gated resource kind only when that feature is usable on the connected Supervisor.
- **G-11** The server's operation catalog (names, inputs, outputs, safety declarations) is a versioned contract. The server MUST report its contract version. Incompatible contract changes MUST ship as new operation names.

### Increment 1: read

- **G-12** The user MUST be able to ask who they are logged in as, which namespace is in use, which contract version and tiers are active, and whether they may perform a given action on a given resource.
- **G-13** The user MUST be able to list and inspect VMs, VM classes, VM images (namespace and cluster), and VM snapshots, with label filtering and paging. Results MUST be concise summaries by default.
- **G-14** The user MUST be able to retrieve recent events for a named VM Service object, newest first, capped in count.
- **G-15** The user MUST be able to request a single diagnosis of a VM. The diagnosis MUST return ranked findings. Each finding identifies the affected object, the reason, a message, and a suggested next step. The diagnosis MUST cover, at minimum:
  - every VM condition that is not satisfied;
  - recent warning events;
  - whether the referenced VM class and VM image exist and are ready;
  - volume claim status;
  - bootstrap readiness, as reported by VM Operator itself;
  - network readiness;
  - whether the VM is paused, pending a power cycle, or still in guest customization.
- **G-16** The user MUST be able to ask what a specific API field means and get back only that field's description, type, allowed values, default, and immediate children.
- **G-17** Every response MUST fit within a documented size limit. Truncated responses MUST say so and tell the user how to get the rest.

### Increment 2: write (opt-in)

- **G-18** The user MUST be able to power a VM on, off, or suspend it, and choose the power-operation mode appropriate to the target state.
- **G-19** The user MUST be able to restart a VM. The server MUST declare restart as non-idempotent.
- **G-20** Power and restart MUST refuse to act on a VM whose power state is managed by a VM group. The refusal MUST point to the group, unless the user explicitly overrides.
- **G-21** The user MUST be able to create a VM from a constrained set of inputs: name, class, image, storage class, optional network interface names, optional existing bootstrap Secret references, power state, and labels. Before any object is persisted, the server MUST:
  - run pre-checks: the class exists, the image resolves, the storage class is available to the namespace, and referenced volume claims exist;
  - run a Supervisor-side dry run;
  - show the user the object as the Supervisor would admit it.

  Creation MUST happen only on a separate, non-dry-run request.
- **G-22** All write operations MUST support dry run. They MUST NOT overwrite concurrent changes made by other writers.
- **G-23** The user MUST be able to wait, with a bounded timeout, for a VM to reach a power state or condition. This avoids repeated polling.
- **G-24** The user MUST be able to see which storage classes the namespace can use.

### Quality

- **G-25** The feature MUST be exercised against a real Supervisor as a non-administrative user. The read operations are exercised in Increment 1. The dry-run create and power operations are exercised in Increment 2.

## Non-goals

- Running the MCP server inside the Supervisor, or serving multiple users from one server. This needs a Supervisor identity integration and is a future spec.
- Any network-listening transport, including one bound to the local machine. Increment 1 and Increment 2 use only the process-pipe transport that AI assistants launch directly.
- Direct vCenter access of any kind.
- Destructive operations: deleting VMs or snapshots, reverting snapshots. These are a follow-up spec that must add server-side confirmation.
- VM resize, snapshot creation, replica set scaling, VM service creation, VM publishing, VM group operations, web console access, and support bundles. These are follow-up work.
- Creating, reading, or modifying Secrets or ConfigMaps, for example to author cloud-init content.
- Accepting an arbitrary VM manifest as input.
- VM Service API versions other than the one the server is built for.
- Any AI or model logic inside the server. The server is deterministic.
- Changes to VM Operator's controllers, webhooks, CRDs, or cluster behavior.

## User stories / acceptance criteria

### US1: DevOps user: inventory and identity (Increment 1)

- **Given** a DevOps user logged in to a Supervisor namespace, **When** the assistant asks "who am I", **Then** the response shows:
  - the user's identity and groups,
  - the namespace,
  - the Supervisor endpoint,
  - the served VM Service API versions,
  - the contract version,
  - that only the read tier is active.
- **Given** that user, **When** the assistant lists VMs, **Then** each VM is summarized by name, desired and observed power state, class, image, primary IP addresses, zone, readiness, and age. No `managedFields`, last-applied annotations, or bootstrap payloads appear.
- **Given** a namespace with more VMs than the requested page size, **When** the assistant lists VMs, **Then** the response contains a continuation cursor, and following it returns the next page.
- **Given** VM images exist both in the namespace and cluster-wide, **When** the assistant lists images, **Then** both are returned under one cursor, with namespace images first.
- **Given** the user lacks access to namespace `prod`, **When** the assistant lists VMs in `prod`, **Then** the result is an error that carries the Supervisor's forbidden message.
- **Given** the server was launched with a namespace restriction that excludes `prod`, **When** the assistant lists VMs in `prod`, **Then** the server refuses before contacting the Supervisor. Cluster-scoped image lookups still succeed.
- **Given** any read operation runs, **Then** no request against Secrets or ConfigMaps is issued. This is verified by test instrumentation.

### US2: DevOps user: diagnose a VM (Increment 1)

- **Given** a VM whose image reference does not resolve, **When** the assistant requests a diagnosis, **Then** the top finding identifies the image readiness failure, cites the image reference, and suggests listing available images. It also states that the image cannot be changed in place and the VM must be recreated.
- **Given** a VM whose bootstrap Secret is missing a required key, **When** diagnosed, **Then** a finding reports bootstrap not ready with VM Operator's reason, and names the Secret reference (kind, name, key) without reading the Secret.
- **Given** a VM that is ready and powered on, **When** diagnosed, **Then** the result reports no blocking findings.
- **Given** a VM with cloud-init inline content, **When** retrieved or diagnosed, **Then** the response reports that inline bootstrap content is present at the relevant field path, and never includes the content.

### US3: DevOps user: explain the API (Increment 1)

- **Given** a question about `spec.powerOffMode`, **When** the assistant asks for an explanation of that field path, **Then** only that field's description, type, allowed values (`Hard`, `Soft`, `TrySoft`), and default are returned.
- **Given** the Supervisor does not permit the user to read its API schema, **When** an explanation is requested, **Then** a clear error says the schema is unavailable.

### US4: DevOps user: operate VMs (Increment 2, opt-in)

- **Given** the server was launched without the write opt-in, **When** the assistant lists available operations, **Then** no write operation is present.
- **Given** the write opt-in, **When** the assistant powers off a VM with mode `Soft`, **Then** the VM's desired power state becomes `PoweredOff` with power-off mode `Soft`. A second identical request changes nothing.
- **Given** a VM that belongs to a VM group, **When** the assistant attempts to power it off without override, **Then** the request is refused and the group is named.
- **Given** the write opt-in, **When** the assistant restarts a VM, **Then** exactly one restart is requested per call.
- **Given** a concurrent change to the VM between read and write, **When** a power operation is submitted, **Then** the server re-reads, re-checks preconditions, and retries a bounded number of times. It never overwrites the concurrent change.
- **Given** the write opt-in and a create request naming a class that does not exist, **When** the assistant dry-runs the creation, **Then** the pre-check reports the missing class before any Supervisor-side dry run, and lists available classes.
- **Given** a valid create request with dry run, **Then** the response shows the VM as the Supervisor would admit it (resolved image and class, defaulted fields), and nothing is persisted.
- **Given** a successful dry run and a subsequent non-dry-run request, **Then** the VM is created, and the assistant can wait for it to become powered on within a bounded timeout.

### CSP admin

- **Given** a CSP admin evaluating the feature, **Then** the documentation states:
  - that nothing is installed on the Supervisor;
  - which Supervisor / VM Operator releases each server release supports;
  - that all actions are audited under the user's own identity.

### Partner engineer

- **Given** a partner building on the operation catalog, **When** a new server release changes an operation incompatibly, **Then** the change appears as a new operation name, with the old name retained and marked deprecated, and the contract version changes accordingly.

## Open questions

- [NEEDS CLARIFICATION: Can the Supervisor DevOps role read the cluster-scoped `Capabilities` resource? If not, what signal should the server use to decide whether feature-gated kinds (VM snapshots) are usable (G-10)? Owner: VM Operator team. Blocks T030.]
- [NEEDS CLARIFICATION: When the VM snapshot feature is off on a real Supervisor, is the snapshot kind still served, and does admission fail closed? Owner: VM Operator team. Blocks T030.]
- [NEEDS CLARIFICATION: Does the Supervisor DevOps role grant read access to the API schema (OpenAPI v3) endpoint (G-16)? Owner: VM Operator team; verify in the Increment 1 E2E (T060).]
- [NEEDS CLARIFICATION: Which Supervisor / VM Operator releases (served API versions) must the first server release support (G-9)? Owner: VM Operator PM.]
- [NEEDS CLARIFICATION: Distribution channel. Downloadable release binaries only (proposed), or also install from source? Owner: VM Operator team. Blocks T090.]
- [NEEDS CLARIFICATION: How does a DevOps user discover usable storage classes (G-24)? Through the namespace resource quota (proposed), or through the storage-policy quota API? Owner: VM Operator / storage team. Blocks T071.]
- [NEEDS CLARIFICATION: Epic ticket to be filed; story tickets per task to be filed and linked to the epic.]
