// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package fakeenv builds a vmop-mcp tool environment backed by a
// controller-runtime fake client, and connects an MCP client to tools over
// in-memory transports. It is used by unit tests and does not import ginkgo
// or gomega.
package fakeenv

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/kube"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/toolkit"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/tools/events"
)

// DefaultNamespace is the default namespace of a fake environment.
const DefaultNamespace = "dev"

// Options configures a fake environment.
type Options struct {
	// Objects are preloaded into the fake client.
	Objects []ctrlclient.Object

	// EnableWrite enables the write tier.
	EnableWrite bool

	// Namespaces is the namespace allow-list.
	Namespaces []string

	// Capabilities overrides the detected capabilities.
	Capabilities map[string]string

	// Interceptor functions run beneath the guard and above the Secret
	// trap. Any nil function falls through.
	Interceptor interceptor.Funcs

	// KubeObjects are preloaded into the fake clientset.
	KubeObjects []runtime.Object
}

// Env is a fake vmop-mcp environment.
type Env struct {
	*toolkit.Env

	// Client is the raw fake client, beneath the guard.
	Client ctrlclient.WithWatch

	// Kube is the fake clientset.
	Kube *k8sfake.Clientset

	// Trap records any request against Secrets or ConfigMaps that got past
	// the guard.
	Trap *kube.SecretTrap
}

// New returns a fake environment.
func New(opts Options) *Env {
	trap := kube.NewSecretTrap()
	inner := fake.NewClientBuilder().
		WithScheme(kube.NewScheme()).
		WithObjects(opts.Objects...).
		WithStatusSubresource(
			&vmopv1.VirtualMachine{},
			&vmopv1.VirtualMachineImage{},
			&vmopv1.ClusterVirtualMachineImage{},
			&vmopv1.VirtualMachineSnapshot{},
		).
		WithIndex(&corev1.Event{}, events.FieldInvolvedObjectKind, func(o ctrlclient.Object) []string {
			return []string{o.(*corev1.Event).InvolvedObject.Kind}
		}).
		WithIndex(&corev1.Event{}, events.FieldInvolvedObjectName, func(o ctrlclient.Object) []string {
			return []string{o.(*corev1.Event).InvolvedObject.Name}
		}).
		WithInterceptorFuncs(trap.Funcs()).
		Build()

	c := interceptor.NewClient(inner, opts.Interceptor)
	kc := k8sfake.NewClientset(opts.KubeObjects...)

	caps := opts.Capabilities
	if caps == nil {
		caps = map[string]string{toolkit.CapabilityVMSnapshots: toolkit.CapabilityActive}
	}
	p := kube.NewStaticProvider(&kube.Clients{Client: c, Kube: kc}, kube.Info{
		Context:        "fake-context",
		Namespace:      DefaultNamespace,
		Server:         "https://fake.example.com",
		KubeconfigUser: "fake-user",
	})
	return &Env{
		Env: &toolkit.Env{
			Provider:       p,
			Namespaces:     toolkit.NewNamespacePolicy(DefaultNamespace, opts.Namespaces),
			EnableWrite:    opts.EnableWrite,
			ServedVersions: []string{contract.BuiltForAPIVersion},
			Capabilities:   caps,
		},
		Client: inner,
		Kube:   kc,
		Trap:   trap,
	}
}

// KubeViolations returns the fake clientset actions that touched Secrets or
// ConfigMaps. The typed clientset is not wrapped by the guard, so tests use
// this as a backstop.
func (e *Env) KubeViolations() []string {
	var out []string
	for _, a := range e.Kube.Actions() {
		switch a.GetResource().Resource {
		case "secrets", "configmaps":
			out = append(out, a.GetVerb()+" "+a.GetResource().Resource)
		}
	}
	return out
}

// Connect builds an MCP server with register and returns a connected client
// session. Close the session when done.
func Connect(ctx context.Context, server *mcp.Server) (*mcp.ClientSession, error) {
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "fakeenv-client", Version: "test"}, nil)
	return client.Connect(ctx, ct, nil)
}

// ConnectTools registers tools with register on a fresh server and connects
// to it.
func (e *Env) ConnectTools(ctx context.Context, register ...func(*toolkit.Registrar)) (*mcp.ClientSession, *toolkit.Registrar, error) {
	s := mcp.NewServer(&mcp.Implementation{Name: "vmop-mcp", Version: "test"}, nil)
	r := toolkit.NewRegistrar(s, e.Env)
	for _, reg := range register {
		reg(r)
	}
	cs, err := Connect(ctx, s)
	return cs, r, err
}

// Result is the decoded result of a tool call.
type Result[T any] struct {
	// Out is the decoded structured content of a successful call.
	Out T

	// Err is the decoded ToolError of a failed call.
	Err *contract.ToolError

	// Text is the first text content block.
	Text string

	// Raw is the raw result.
	Raw *mcp.CallToolResult
}

// Call invokes a tool and decodes the result. A non-nil error means a
// protocol failure, not a tool error.
func Call[T any](ctx context.Context, cs *mcp.ClientSession, name string, args any) (Result[T], error) {
	var r Result[T]
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return r, err
	}
	r.Raw = res
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			r.Text = tc.Text
		}
	}
	if res.IsError {
		te := &contract.ToolError{}
		if err := json.Unmarshal([]byte(r.Text), te); err != nil {
			// Errors raised by the SDK itself, such as input validation,
			// are plain text.
			te = &contract.ToolError{Code: "protocol", Message: r.Text}
		}
		r.Err = te
		return r, nil
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return r, fmt.Errorf("re-encoding structured content: %w", err)
	}
	if err := json.Unmarshal(b, &r.Out); err != nil {
		return r, fmt.Errorf("decoding structured content: %w", err)
	}
	return r, nil
}

// VM returns a minimal VirtualMachine in the default namespace.
func VM(name string) *vmopv1.VirtualMachine {
	return &vmopv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         DefaultNamespace,
			Name:              name,
			CreationTimestamp: metav1.Now(),
		},
		Spec: vmopv1.VirtualMachineSpec{
			ClassName:    "small",
			ImageName:    "vmi-0123456789abcdef0",
			StorageClass: "wcp-storage",
			PowerState:   vmopv1.VirtualMachinePowerStateOn,
		},
	}
}
