// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package vm implements the VirtualMachine read tools.
package vm

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

// Register registers the VirtualMachine read tools.
func Register(r *toolkit.Registrar) {
	env := r.Env

	toolkit.AddRead(r, toolkit.Spec[contract.VirtualMachineList]{
		Name:  "list_virtual_machines",
		Title: "List VMs",
		Description: "Lists VirtualMachines in a namespace with their desired and observed power state, class, " +
			"image, primary IPs, zone, and readiness. Supports label selectors and paging.",
		Summary: func(l contract.VirtualMachineList) string {
			var b strings.Builder
			fmt.Fprintf(&b, "%d VMs", len(l.Items))
			for _, s := range l.Items {
				b.WriteString("\n")
				b.WriteString(SummaryLine(s))
			}
			toolkit.WritePage(&b, l.Page)
			return b.String()
		},
	}, func(ctx context.Context, in contract.ListInput) (contract.VirtualMachineList, error) {
		return list(ctx, env, in)
	})

	toolkit.AddRead(r, toolkit.Spec[contract.VirtualMachineDetail]{
		Name:  "get_virtual_machine",
		Title: "Get VM",
		Description: "Gets one VirtualMachine: summary, conditions, storage, volumes, network interfaces, " +
			"a bootstrap summary (providers and Secret references only), extraConfig keys, and identifiers.",
		Summary: func(d contract.VirtualMachineDetail) string {
			var b strings.Builder
			b.WriteString(SummaryLine(d.VirtualMachineSummary))
			for _, c := range d.Conditions {
				fmt.Fprintf(&b, "\n  %s=%s %s", c.Type, c.Status, c.Reason)
			}
			return b.String()
		},
	}, func(ctx context.Context, in contract.GetInput) (contract.VirtualMachineDetail, error) {
		vm, err := Get(ctx, env, in.Namespace, in.Name)
		if err != nil {
			return contract.VirtualMachineDetail{}, err
		}
		return projection.VirtualMachineDetail(vm), nil
	})
}

// Get fetches a VirtualMachine after resolving its namespace.
func Get(ctx context.Context, env *toolkit.Env, namespace, name string) (*vmopv1.VirtualMachine, error) {
	ns, err := env.Namespaces.Resolve(namespace)
	if err != nil {
		return nil, err
	}
	return kube.Do(ctx, env.Provider, func(c *kube.Clients) (*vmopv1.VirtualMachine, error) {
		vm := &vmopv1.VirtualMachine{}
		if err := c.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: ns, Name: name}, vm); err != nil {
			return nil, err
		}
		return vm, nil
	})
}

func list(ctx context.Context, env *toolkit.Env, in contract.ListInput) (contract.VirtualMachineList, error) {
	out := contract.VirtualMachineList{Items: []contract.VirtualMachineSummary{}}
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
	l, err := kube.Do(ctx, env.Provider, func(c *kube.Clients) (*vmopv1.VirtualMachineList, error) {
		l := &vmopv1.VirtualMachineList{}
		return l, c.Client.List(ctx, l, opts...)
	})
	if err != nil {
		return out, err
	}
	for i := range l.Items {
		out.Items = append(out.Items, projection.VirtualMachineSummary(&l.Items[i]))
	}
	var truncated bool
	if out.Items, truncated = toolkit.Fit(out.Items); truncated {
		out.Truncated = true
		out.Hint = toolkit.TruncationHint(len(out.Items))
	} else if l.Continue != "" {
		out.NextCursor = contract.EncodeCursor("", l.Continue)
	}
	return out, nil
}

// SummaryLine renders a one-line summary of a VM.
func SummaryLine(s contract.VirtualMachineSummary) string {
	ip := ""
	if s.PrimaryIP4 != nil {
		ip = s.PrimaryIP4.Value
	}
	return fmt.Sprintf("%s/%s power=%s/%s class=%s image=%s ip=%s ready=%s age=%s",
		s.Namespace, s.Name, s.PowerState.Desired, s.PowerState.Observed,
		s.ClassName, s.Image.Name, ip, s.Ready, s.Age)
}
