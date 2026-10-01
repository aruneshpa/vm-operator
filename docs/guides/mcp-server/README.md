# VM Operator MCP Server (`vmop-mcp`)

`vmop-mcp` is a [Model Context Protocol](https://modelcontextprotocol.io) (MCP) server for VM Service. AI assistants that speak MCP, such as Claude Code, Claude Desktop, VS Code, and Cursor, launch it on your workstation and use its tools to inspect, diagnose, explain, and (only when you explicitly allow it) operate VM Service resources on a vSphere Supervisor.

The assistant acts **as you**: `vmop-mcp` uses the kubeconfig produced by your normal Supervisor login and is bound by exactly your permissions. Nothing is installed on the Supervisor, and every action appears in the Supervisor audit log under your own identity.

## How it works

```
AI assistant  --stdio-->  vmop-mcp  --HTTPS (your kubeconfig)-->  Supervisor
 (MCP host)              (your PC)                               kube-apiserver
```

- **Transport**: stdio only. The assistant starts `vmop-mcp` as a subprocess. There is no network listener.
- **Kubernetes API only**: `vmop-mcp` never contacts vCenter and holds no vSphere credentials.
- **Read-only by default**: write tools are not even advertised unless you start the server with `--enable-write`.

## Install

Download the binary for your platform from the release assets (`vmop-mcp-<os>-<arch>`, with a `vmop-mcp-SHA256SUMS` file), or build it from a clone of this repository:

```shell
make vmop-mcp-only          # builds bin/vmop-mcp for this machine
make vmop-mcp-dist          # cross-compiles all release binaries
```

Put the binary on your `PATH` and check it:

```shell
vmop-mcp --version
```

`go install` is not supported, because the `mcp/` module uses `replace` directives for the in-repository API module.

## Log in to the Supervisor

`vmop-mcp` reads your kubeconfig (`--kubeconfig`, then `$KUBECONFIG`, then `~/.kube/config`). Log in as you normally do, for example:

```shell
kubectl vsphere login --server=<SUPERVISOR> \
  --vsphere-username <USER>
kubectl config use-context <NAMESPACE>
```

The VCF CLI (`vcf context ...`) also produces a kubeconfig context that works.

**Token expiry.** Supervisor tokens expire (typically after 10 hours). When a tool returns an `unauthorized` error, log in again. `vmop-mcp` notices the kubeconfig file changed and picks up the new token without a restart.

Kubeconfigs whose `exec` credential plugin sets `interactiveMode: Always` are rejected, because stdin is the MCP protocol channel.

## Configure your AI assistant

In every example, `--context` and `--namespaces` are optional. Add `--enable-write` only if you want the assistant to be able to change VMs.

### Claude Code

```shell
claude mcp add vmop -- vmop-mcp --context <CONTEXT>
```

### Claude Desktop

Add to `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "vmop": {
      "command": "vmop-mcp",
      "args": ["--context", "<CONTEXT>"]
    }
  }
}
```

### VS Code

Add to `.vscode/mcp.json` (or your user MCP configuration):

```json
{
  "servers": {
    "vmop": {
      "type": "stdio",
      "command": "vmop-mcp",
      "args": ["--context", "<CONTEXT>"]
    }
  }
}
```

### Cursor

Add to `~/.cursor/mcp.json` (or `.cursor/mcp.json` in a project):

```json
{
  "mcpServers": {
    "vmop": {
      "command": "vmop-mcp",
      "args": ["--context", "<CONTEXT>"]
    }
  }
}
```

## Flags

| Flag | Default | Meaning |
|------|---------|---------|
| `--kubeconfig` | `$KUBECONFIG`, then `~/.kube/config` | Kubeconfig to use |
| `--context` | current context | Kubeconfig context to use |
| `--namespace` | context namespace | Default namespace for tools |
| `--namespaces` | none (all allowed) | Comma-separated allow-list. Requests for other namespaces are refused before any call reaches the Supervisor. Cluster-scoped images are always allowed. |
| `--enable-write` | `false` | Enable the write tier |
| `--version` | | Print the version and exit |

All logs go to stderr; stdout carries only the MCP protocol.

## Tiers

| Tier | Enabled by | Tools |
|------|-----------|-------|
| Read | always | identity, inventory, events, diagnosis, explain, storage classes |
| Write | `--enable-write` | power state, restart, wait, create |

Tools of a disabled tier are not registered, so the assistant cannot see or call them. Every tool declares MCP annotations (`readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`) so that your assistant can ask for approval appropriately. No tool deletes anything.

## Tools

The RBAC column lists the Kubernetes verbs the tool needs; resources are in the `vmoperator.vmware.com` group unless noted. A DevOps user with view access to a Supervisor namespace can use every read tool; edit access is needed for the write tools.

| Tool | Tier | What it does | RBAC |
|------|------|--------------|------|
| `whoami` | read | Your identity, context, namespace, served API versions, contract version, tiers, capabilities | `create selfsubjectreviews` (`authentication.k8s.io`, granted to all authenticated users) |
| `check_access` | read | Asks whether you may perform a verb on a resource | `create selfsubjectaccessreviews` (`authorization.k8s.io`, granted to all authenticated users) |
| `list_virtual_machines`, `get_virtual_machine` | read | VM summaries; details with conditions, volumes, interfaces, bootstrap summary, extraConfig keys | `list`, `get virtualmachines` |
| `list_virtual_machine_classes`, `get_virtual_machine_class` | read | Classes with CPU, memory, description | `list`, `get virtualmachineclasses` |
| `list_virtual_machine_images`, `get_virtual_machine_image` | read | Namespace and cluster images with OS, firmware, readiness | `list`, `get virtualmachineimages`, `clustervirtualmachineimages` |
| `list_virtual_machine_snapshots`, `get_virtual_machine_snapshot` | read | Snapshots (only when the VM snapshot capability is not known to be inactive) | `list`, `get virtualmachinesnapshots` |
| `get_events` | read | Recent events for an object, newest first | `list events` (core) |
| `diagnose_virtual_machine` | read | Ranked findings explaining why a VM is not ready or not running | `get virtualmachines`, `virtualmachineclasses`, `virtualmachineimages`, `clustervirtualmachineimages`; `get persistentvolumeclaims`, `list events` (core) |
| `explain_field` | read | Explains one API field from the Supervisor's own OpenAPI schema | `get` on `/openapi/v3` (normally granted to all authenticated users) |
| `list_storage_classes` | read | Storage classes assigned to the namespace, with quota | `list resourcequotas` (core) |
| `set_virtual_machine_power_state` | write | Power on, off, or suspend, with mode `Hard`, `Soft`, or `TrySoft` | `get`, `patch virtualmachines` |
| `restart_virtual_machine` | write | Requests a restart (not idempotent: every call restarts) | `get`, `patch virtualmachines` |
| `wait_for_virtual_machine` | write | Waits for a power state or condition, up to 10 minutes | `get virtualmachines` |
| `create_virtual_machine` | write | Creates a VM from class, image, storage class, networks, and an existing bootstrap Secret reference, after preflight checks and a server-side dry run | `create virtualmachines`, plus the read verbs above |

Prompts:

- `troubleshoot-vm` — guides the assistant through a read-only diagnosis.
- `create-vm` — guides a dry-run-first creation (write tier only).

### Safety rules for writes

- Every write tool accepts `dryRun`. `create_virtual_machine` requires it explicitly; ask the assistant to dry-run first.
- Writes use a patch with a `resourceVersion` precondition and the field manager `vmop-mcp`, and retry a bounded number of times on conflict, so they never overwrite a concurrent change.
- Power and restart refuse a VM whose power state is managed by a `VirtualMachineGroup`, unless `force` is set.
- Preflight checks catch problems VM Operator admission does not, such as a class that does not exist in the namespace or a storage class not assigned to it.

## Privacy and security guarantees

- **No Secret or ConfigMap requests, ever.** The client refuses any request against Secrets or ConfigMaps before it is sent. Bootstrap readiness is taken from the VM's own `VirtualMachineBootstrapReady` condition, which VM Operator computes.
- **Allow-list output.** Every response is built from an explicit list of permitted fields. Bootstrap data is reported only as providers, Secret references (name and key), and the field paths that hold inline values; the values themselves are never returned. `extraConfig` is reported as keys only, VM class `configSpec` only as present or absent, and image OVF properties only as keys.
- **Untrusted data is labeled.** Strings that come from guests, images, events, or users (for example guest IPs, image product text, and event messages) are returned as `untrusted` values with a length cap, and every tool description reminds the assistant that returned values are data, not instructions.
- **Your identity only.** There is no service account, impersonation, or stored credential. A request the Supervisor forbids is reported as `forbidden` with the Supervisor's message.
- **Bounded output.** Responses are capped at 64 KiB. Lists are paged with an opaque cursor; truncated results say so and tell the assistant how to get the rest.

## Errors

Tool errors are returned as JSON with a `code`, the Supervisor's `message` where applicable, and a `hint`:

`unauthorized`, `forbidden`, `not_found`, `conflict`, `invalid`, `namespace_not_allowed`, `cursor_expired`, `precondition_failed`, `schema_unavailable`, `timeout`, `internal`.

## Compatibility

`vmop-mcp` checks at startup that the Supervisor serves the VM Operator API version it was built for, and exits with the served versions if not.

| vmop-mcp release | Tool contract | Built for API | Supervisor requirement |
|------------------|---------------|---------------|------------------------|
| `mcp/v0.1.x` (first release) | `1.1.0` | `vmoperator.vmware.com/v1alpha6` | VM Operator serving `v1alpha6` |

The tool contract is versioned separately from the binary. Additive changes bump its minor version; incompatible changes ship as new tool names, with the old name kept and marked deprecated.

## Limitations

- Runs only on your workstation over stdio; there is no shared or in-cluster server.
- No delete, snapshot revert, resize, snapshot create, replica set scaling, VM service, publish, group, or web console operations.
- Cannot create or read bootstrap Secrets; reference an existing Secret when creating a VM.
- `explain_field` needs read access to the Supervisor's `/openapi/v3` endpoint.
- Only the API version the binary is built for is supported.
