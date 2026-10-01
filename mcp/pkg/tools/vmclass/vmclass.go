// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package vmclass implements the VirtualMachineClass read tools.
package vmclass

import (
	"context"
	"fmt"
	"strings"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/kube"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/projection"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/toolkit"
)

// Register registers the VirtualMachineClass read tools.
func Register(r *toolkit.Registrar) {
	env := r.Env

	toolkit.AddRead(r, toolkit.Spec[contract.VirtualMachineClassList]{
		Name:        "list_virtual_machine_classes",
		Title:       "List VM classes",
		Description: "Lists the VirtualMachineClasses available in a namespace with CPU, memory, and description.",
		Summary: func(l contract.VirtualMachineClassList) string {
			var b strings.Builder
			fmt.Fprintf(&b, "%d classes", len(l.Items))
			for _, c := range l.Items {
				fmt.Fprintf(&b, "\n%s cpus=%d memory=%s %s", c.Name, c.CPUs, c.Memory, c.Description)
			}
			toolkit.WritePage(&b, l.Page)
			return b.String()
		},
	}, func(ctx context.Context, in contract.ListInput) (contract.VirtualMachineClassList, error) {
		out := contract.VirtualMachineClassList{Items: []contract.VirtualMachineClassSummary{}}
		ns, err := env.Namespaces.Resolve(in.Namespace)
		if err != nil {
			return out, err
		}
		cur, err := contract.DecodeCursor(in.Cursor)
		if err != nil {
			return out, err
		}
		opts, err := toolkit.ListOptions(ns, in, cur.Token)
		if err != nil {
			return out, err
		}
		l, err := kube.Do(ctx, env.Provider, func(c *kube.Clients) (*vmopv1.VirtualMachineClassList, error) {
			l := &vmopv1.VirtualMachineClassList{}
			return l, c.Client.List(ctx, l, opts...)
		})
		if err != nil {
			return out, err
		}
		for i := range l.Items {
			out.Items = append(out.Items, projection.VirtualMachineClass(&l.Items[i]))
		}
		var truncated bool
		if out.Items, truncated = toolkit.Fit(out.Items); truncated {
			out.Truncated, out.Hint = true, toolkit.TruncationHint(len(out.Items))
		} else if l.Continue != "" {
			out.NextCursor = contract.EncodeCursor("", l.Continue)
		}
		return out, nil
	})

	toolkit.AddRead(r, toolkit.Spec[contract.VirtualMachineClassSummary]{
		Name:        "get_virtual_machine_class",
		Title:       "Get VM class",
		Description: "Gets one VirtualMachineClass. The class configSpec is never returned; hasConfigSpec reports its presence.",
		Summary: func(c contract.VirtualMachineClassSummary) string {
			return fmt.Sprintf("%s cpus=%d memory=%s hasConfigSpec=%t %s", c.Name, c.CPUs, c.Memory, c.HasConfigSpec, c.Description)
		},
	}, func(ctx context.Context, in contract.GetInput) (contract.VirtualMachineClassSummary, error) {
		c, err := Get(ctx, env, in.Namespace, in.Name)
		if err != nil {
			return contract.VirtualMachineClassSummary{}, err
		}
		return projection.VirtualMachineClass(c), nil
	})
}

// Get fetches a VirtualMachineClass after resolving its namespace.
func Get(ctx context.Context, env *toolkit.Env, namespace, name string) (*vmopv1.VirtualMachineClass, error) {
	ns, err := env.Namespaces.Resolve(namespace)
	if err != nil {
		return nil, err
	}
	return kube.Do(ctx, env.Provider, func(c *kube.Clients) (*vmopv1.VirtualMachineClass, error) {
		obj := &vmopv1.VirtualMachineClass{}
		return obj, c.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: ns, Name: name}, obj)
	})
}
