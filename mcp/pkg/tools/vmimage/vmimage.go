// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package vmimage implements the VirtualMachineImage and
// ClusterVirtualMachineImage read tools.
package vmimage

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

// List scopes.
const (
	ScopeAll       = "all"
	ScopeNamespace = "namespace"
	ScopeCluster   = "cluster"

	phaseNamespace = "ns"
	phaseCluster   = "cluster"
)

// ListInput is the input of list_virtual_machine_images.
type ListInput struct {
	contract.ListInput
	Scope string `json:"scope,omitempty" jsonschema:"Which images to list. Default all: namespace images first, then cluster images."`
}

// GetInput is the input of get_virtual_machine_image.
type GetInput struct {
	Kind      string `json:"kind" jsonschema:"Image kind."`
	Namespace string `json:"namespace,omitempty" jsonschema:"Namespace of a VirtualMachineImage. Ignored for ClusterVirtualMachineImage."`
	Name      string `json:"name" jsonschema:"Image object name, e.g. vmi-0123456789abcdef."`
}

// Register registers the image read tools.
func Register(r *toolkit.Registrar) {
	env := r.Env

	toolkit.AddRead(r, toolkit.Spec[contract.VirtualMachineImageList]{
		Name:  "list_virtual_machine_images",
		Title: "List VM images",
		Description: "Lists VirtualMachineImages in a namespace and cluster-wide ClusterVirtualMachineImages with " +
			"display name, OS, firmware, and readiness. Use the object name (not the display name) in other tools.",
		Enums: map[string][]string{"scope": {ScopeAll, ScopeNamespace, ScopeCluster}},
		Summary: func(l contract.VirtualMachineImageList) string {
			var b strings.Builder
			fmt.Fprintf(&b, "%d images", len(l.Items))
			for _, i := range l.Items {
				dn := ""
				if i.DisplayName != nil {
					dn = i.DisplayName.Value
				}
				fmt.Fprintf(&b, "\n%s %s display=%q os=%s/%s ready=%s", i.Kind, i.Name, dn, i.OSInfo.Type, i.OSInfo.Version, i.Ready)
			}
			toolkit.WritePage(&b, l.Page)
			return b.String()
		},
	}, func(ctx context.Context, in ListInput) (contract.VirtualMachineImageList, error) {
		return list(ctx, env, in)
	})

	toolkit.AddRead(r, toolkit.Spec[contract.VirtualMachineImageDetail]{
		Name:        "get_virtual_machine_image",
		Title:       "Get VM image",
		Description: "Gets one VirtualMachineImage or ClusterVirtualMachineImage. OVF property values are never returned, only keys.",
		Enums:       map[string][]string{"kind": {projection.KindVirtualMachineImage, projection.KindClusterVirtualMachineImage}},
		Summary: func(d contract.VirtualMachineImageDetail) string {
			return fmt.Sprintf("%s %s os=%s/%s firmware=%s ready=%s", d.Kind, d.Name, d.OSInfo.Type, d.OSInfo.Version, d.Firmware, d.Ready)
		},
	}, func(ctx context.Context, in GetInput) (contract.VirtualMachineImageDetail, error) {
		return get(ctx, env, in)
	})
}

// Lookup finds an image by kind and name. For a VirtualMachineImage the
// namespace must already be resolved. The returned status is nil if the image
// does not exist.
func Lookup(ctx context.Context, env *toolkit.Env, kind, ns, name string) (*vmopv1.VirtualMachineImageStatus, error) {
	return kube.Do(ctx, env.Provider, func(c *kube.Clients) (*vmopv1.VirtualMachineImageStatus, error) {
		switch kind {
		case projection.KindClusterVirtualMachineImage:
			obj := &vmopv1.ClusterVirtualMachineImage{}
			if err := c.Client.Get(ctx, ctrlclient.ObjectKey{Name: name}, obj); err != nil {
				return nil, err
			}
			return &obj.Status, nil
		default:
			obj := &vmopv1.VirtualMachineImage{}
			if err := c.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: ns, Name: name}, obj); err != nil {
				return nil, err
			}
			return &obj.Status, nil
		}
	})
}

func get(ctx context.Context, env *toolkit.Env, in GetInput) (contract.VirtualMachineImageDetail, error) {
	if in.Kind == projection.KindClusterVirtualMachineImage {
		obj, err := kube.Do(ctx, env.Provider, func(c *kube.Clients) (*vmopv1.ClusterVirtualMachineImage, error) {
			obj := &vmopv1.ClusterVirtualMachineImage{}
			return obj, c.Client.Get(ctx, ctrlclient.ObjectKey{Name: in.Name}, obj)
		})
		if err != nil {
			return contract.VirtualMachineImageDetail{}, err
		}
		return projection.ImageDetail(in.Kind, obj.ObjectMeta, &obj.Status), nil
	}
	ns, err := env.Namespaces.Resolve(in.Namespace)
	if err != nil {
		return contract.VirtualMachineImageDetail{}, err
	}
	obj, err := kube.Do(ctx, env.Provider, func(c *kube.Clients) (*vmopv1.VirtualMachineImage, error) {
		obj := &vmopv1.VirtualMachineImage{}
		return obj, c.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: ns, Name: in.Name}, obj)
	})
	if err != nil {
		return contract.VirtualMachineImageDetail{}, err
	}
	return projection.ImageDetail(projection.KindVirtualMachineImage, obj.ObjectMeta, &obj.Status), nil
}

func list(ctx context.Context, env *toolkit.Env, in ListInput) (contract.VirtualMachineImageList, error) {
	out := contract.VirtualMachineImageList{Items: []contract.VirtualMachineImageSummary{}}
	scope := in.Scope
	if scope == "" {
		scope = ScopeAll
	}
	cur, err := contract.DecodeCursor(in.Cursor)
	if err != nil {
		return out, err
	}
	phase := cur.Phase
	if phase == "" {
		phase = phaseNamespace
		if scope == ScopeCluster {
			phase = phaseCluster
		}
	}
	limit := toolkit.ListLimit(in.Limit)

	var next string
	if phase == phaseNamespace {
		ns, err := env.Namespaces.Resolve(in.Namespace)
		if err != nil {
			return out, err
		}
		opts, err := toolkit.ListOptions(ns, in.ListInput, cur.Token)
		if err != nil {
			return out, err
		}
		l, err := kube.Do(ctx, env.Provider, func(c *kube.Clients) (*vmopv1.VirtualMachineImageList, error) {
			l := &vmopv1.VirtualMachineImageList{}
			return l, c.Client.List(ctx, l, opts...)
		})
		if err != nil {
			return out, err
		}
		for i := range l.Items {
			img := &l.Items[i]
			out.Items = append(out.Items, projection.ImageSummary(projection.KindVirtualMachineImage, img.ObjectMeta, &img.Status))
		}
		switch {
		case l.Continue != "":
			next = contract.EncodeCursor(phaseNamespace, l.Continue)
		case scope == ScopeAll:
			// Namespace images are exhausted; continue with cluster images,
			// filling the rest of this page if there is room.
			phase, cur.Token = phaseCluster, ""
			limit -= int64(len(l.Items))
			if limit <= 0 {
				next = contract.EncodeCursor(phaseCluster, "")
			}
		}
	}

	if phase == phaseCluster && next == "" && limit > 0 {
		// Cluster-scoped images are not subject to the namespace allow-list.
		sub := in.ListInput
		sub.Limit = int(limit)
		opts, err := toolkit.ListOptions("", sub, cur.Token)
		if err != nil {
			return out, err
		}
		l, err := kube.Do(ctx, env.Provider, func(c *kube.Clients) (*vmopv1.ClusterVirtualMachineImageList, error) {
			l := &vmopv1.ClusterVirtualMachineImageList{}
			return l, c.Client.List(ctx, l, opts...)
		})
		if err != nil {
			return out, err
		}
		for i := range l.Items {
			img := &l.Items[i]
			out.Items = append(out.Items, projection.ImageSummary(projection.KindClusterVirtualMachineImage, img.ObjectMeta, &img.Status))
		}
		if l.Continue != "" {
			next = contract.EncodeCursor(phaseCluster, l.Continue)
		}
	}

	var truncated bool
	if out.Items, truncated = toolkit.Fit(out.Items); truncated {
		out.Truncated, out.Hint = true, toolkit.TruncationHint(len(out.Items))
	} else {
		out.NextCursor = next
	}
	return out, nil
}
