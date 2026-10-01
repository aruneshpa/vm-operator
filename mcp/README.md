# vmop-mcp (developer notes)

`vmop-mcp` is a client-side Model Context Protocol server for VM Operator. This directory is its own Go module, `github.com/vmware-tanzu/vm-operator/mcp`. User documentation lives in [`docs/guides/mcp-server/README.md`](../docs/guides/mcp-server/README.md). The design is specified in [`.sdd/specs/010-mcp-server/`](../.sdd/specs/010-mcp-server/) (`spec.md`, `plan.md`, `model.md`, `tasks.md`).

## Boundaries

- The module talks only to the Kubernetes API, with the user's kubeconfig.
- It imports the `api` module (`vmopv1` = `v1alpha6`), `external/capabilities`, and `pkg/constants/testlabels` through `replace` directives. It must **not** import the root module (`pkg/`, `controllers/`, `webhooks/`, `services/`) or govmomi; a depguard rule (`mcp-boundary` in `.golangci.yml`) enforces this.
- It never issues requests against Secrets or ConfigMaps (`pkg/kube/guard.go`).
- Tool inputs and outputs are curated DTOs in `pkg/contract`, never `vmopv1` types (JSON Schema inference panics on or mis-types them).

## Layout

```
cmd/vmop-mcp/       flags, stderr logging, stdio transport
pkg/buildinfo/      Version/Commit, set with -ldflags -X
pkg/kube/           kubeconfig provider and reload, guarded client,
                    static REST mapper, served-version check
pkg/capabilities/   Supervisor capability detection
pkg/contract/       tool contract: DTOs, error codes, cursor, Version
pkg/projection/     vmopv1 -> DTO allow-list projection, field inventory
pkg/toolkit/        registrar (tiers, annotations), namespace policy,
                    error mapping, size limits
pkg/diagnose/       pure diagnosis rule engine
pkg/openapi/        field lookup in the Supervisor's OpenAPI v3
pkg/prompts/        MCP prompts
pkg/tools/<name>/   one package per tool family
pkg/server/         server assembly (New, NewWithEnv)
test/fakeenv/       fake-client environment and in-memory MCP client
test/envtest/       envtest API server with VM Operator CRDs and users
```

## Make targets

Run these from the repository root:

| Target | What it does |
|--------|--------------|
| `make vmop-mcp-only` | Builds `bin/vmop-mcp` for the host (honors `GOOS`/`GOARCH`) |
| `make vmop-mcp` | Lints, then builds |
| `make vmop-mcp-dist` | Cross-compiles darwin, linux, and windows on amd64 and arm64 into `bin/vmop-mcp-<os>-<arch>[.exe]`, plus `bin/vmop-mcp-SHA256SUMS` |
| `make test-mcp` | Runs all `mcp/` suites with ginkgo (no coverage; ginkgo finalizes coverage from the root module) |
| `make vulncheck-mcp` | Runs govulncheck on this module (also run by `make vulncheck-go`) |
| `make lint-go` | Lints every module, including this one |
| `make modules` | Runs `go mod tidy` for every module, including this one |

## Testing

- Unit specs carry `Label(testlabels.MCP)`. They use `test/fakeenv`, which builds a controller-runtime fake client with a Secret/ConfigMap trap beneath the guarded client, and connects an MCP client over in-memory transports.
- Integration specs carry `Label(testlabels.MCP, testlabels.EnvTest)`. They use `test/envtest`, which starts kube-apiserver and etcd with the CRDs from `config/crd/bases` and the Capabilities CRD, and creates real authenticated users with namespaced RBAC. VM Operator's webhooks are **not** installed, so only CRD schema validation runs.
- envtest needs the control-plane binaries. `make test-mcp` builds them into `hack/tools/bin/<os>_<arch>` and the root Makefile exports `KUBEBUILDER_ASSETS`. To run a single package directly:

```shell
export KUBEBUILDER_ASSETS="$(pwd)/hack/tools/bin/$(go env GOOS)_$(go env GOARCH)"
go -C mcp test ./pkg/tools/vm/...
```

- VM Operator admission and Supervisor RBAC are covered only by the E2E specs in `test/e2e/vmservice/vmservice/mcp/` (label `mcp`).

## Contract changes

`pkg/server/testdata/tools_list.golden.json` is a golden copy of the full `tools/list` response. A diff in it is a contract change: bump `contract.Version` (minor for additive changes; incompatible changes ship as a new tool name) and update `.sdd/specs/010-mcp-server/model.md`.

## Releases

- Sub-module releases are tagged `mcp/vX.Y.Z`, following the existing `api/vX.Y.Z` convention.
- Binaries are built with `make vmop-mcp-dist` and published as release assets with the SHA256 sums.
- `go install github.com/vmware-tanzu/vm-operator/mcp/cmd/vmop-mcp@...` is not supported while `go.mod` contains `replace` directives.
- Update the compatibility matrix in `docs/guides/mcp-server/README.md` for every release.
