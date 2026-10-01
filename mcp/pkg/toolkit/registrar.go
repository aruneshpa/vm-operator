// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package toolkit provides the shared machinery used by every vmop-mcp tool:
// tier-aware registration with explicit safety annotations, namespace
// policy, error mapping, and output size limits.
package toolkit

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/kube"
)

// Tiers.
const (
	TierRead  = "read"
	TierWrite = "write"
)

// Capability states.
const (
	CapabilityActive   = "active"
	CapabilityInactive = "inactive"
	CapabilityUnknown  = "unknown"
)

// CapabilityVMSnapshots is the capability name for VM snapshots.
const CapabilityVMSnapshots = "vmSnapshots"

// Env is the environment shared by all tools.
type Env struct {
	// Provider supplies the user's clients.
	Provider kube.Provider

	// Namespaces resolves and restricts namespaces.
	Namespaces *NamespacePolicy

	// EnableWrite enables the write tier.
	EnableWrite bool

	// ServedVersions are the VM Operator API versions served by the
	// Supervisor.
	ServedVersions []string

	// Capabilities maps a capability name to its state.
	Capabilities map[string]string
}

// Tiers returns the enabled tiers.
func (e *Env) Tiers() []string {
	if e.EnableWrite {
		return []string{TierRead, TierWrite}
	}
	return []string{TierRead}
}

// Capability returns the state of a capability; absent means unknown.
func (e *Env) Capability(name string) string {
	if s, ok := e.Capabilities[name]; ok {
		return s
	}
	return CapabilityUnknown
}

// Registrar registers tools on an MCP server according to the enabled tiers
// and capabilities.
type Registrar struct {
	Server *mcp.Server
	Env    *Env

	names []string
}

// NewRegistrar returns a new Registrar.
func NewRegistrar(s *mcp.Server, env *Env) *Registrar {
	return &Registrar{Server: s, Env: env}
}

// Names returns the names of the registered tools, sorted.
func (r *Registrar) Names() []string {
	out := append([]string(nil), r.names...)
	sort.Strings(out)
	return out
}

// Spec describes a tool.
type Spec[Out any] struct {
	Name        string
	Title       string
	Description string

	// Enums constrains top-level string input properties to fixed values.
	Enums map[string][]string

	// Idempotent sets idempotentHint for write-tier tools. Read-tier tools
	// are always idempotent.
	Idempotent bool

	// ReadOnly marks a write-tier tool as read-only in its annotations. It
	// is used for tools that only make sense alongside writes.
	ReadOnly bool

	// Summary renders the text content of a successful result. If nil, the
	// compact JSON of the output is used.
	Summary func(Out) string
}

// Handler is a typed tool handler.
type Handler[In, Out any] func(ctx context.Context, in In) (Out, error)

// AddRead registers a read-tier tool.
func AddRead[In, Out any](r *Registrar, spec Spec[Out], h Handler[In, Out]) {
	add(r, spec, h, TierRead)
}

// AddWrite registers a write-tier tool. It is a no-op unless the write tier
// is enabled, so the tool never appears in tools/list otherwise.
func AddWrite[In, Out any](r *Registrar, spec Spec[Out], h Handler[In, Out]) {
	if !r.Env.EnableWrite {
		return
	}
	add(r, spec, h, TierWrite)
}

func annotations(tier string, spec readOnlyIdempotent) *mcp.ToolAnnotations {
	f := false
	if tier == TierRead || spec.readOnly {
		return &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: &f,
			IdempotentHint:  true,
			OpenWorldHint:   &f,
		}
	}
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: &f,
		IdempotentHint:  spec.idempotent,
		OpenWorldHint:   &f,
	}
}

type readOnlyIdempotent struct {
	readOnly   bool
	idempotent bool
}

func add[In, Out any](r *Registrar, spec Spec[Out], h Handler[In, Out], tier string) {
	schema, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("tool %q: input schema: %v", spec.Name, err))
	}
	for prop, values := range spec.Enums {
		ps, ok := schema.Properties[prop]
		if !ok {
			panic(fmt.Sprintf("tool %q: enum for unknown property %q", spec.Name, prop))
		}
		for _, v := range values {
			ps.Enum = append(ps.Enum, v)
		}
	}

	ann := annotations(tier, readOnlyIdempotent{readOnly: spec.ReadOnly, idempotent: spec.Idempotent})
	ann.Title = spec.Title

	tool := &mcp.Tool{
		Name:        spec.Name,
		Title:       spec.Title,
		Description: spec.Description + " " + contract.DataNotInstructions,
		Annotations: ann,
		InputSchema: schema,
	}

	mcp.AddTool(r.Server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := h(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, MapError(err)
		}
		raw, err := json.Marshal(out)
		if err != nil {
			var zero Out
			return nil, zero, contract.NewError(contract.CodeInternal, err.Error(), "")
		}
		if len(raw) > MaxResultBytes {
			var zero Out
			return nil, zero, contract.NewError(contract.CodeInvalid,
				fmt.Sprintf("result is %d bytes, over the %d byte limit", len(raw), MaxResultBytes),
				"narrow the request, e.g. with a label selector or a smaller limit")
		}
		text := string(raw)
		if spec.Summary != nil {
			text = spec.Summary(out)
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, out, nil
	})
	r.names = append(r.names, spec.Name)
}
