// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ErrForbiddenKind is returned by a guarded client for any request against a
// Secret or ConfigMap.
var ErrForbiddenKind = fmt.Errorf("vmop-mcp never accesses Secrets or ConfigMaps")

// IsForbiddenGVK reports whether gvk is a Secret, ConfigMap, or a list of
// either in the core API group.
func IsForbiddenGVK(gvk schema.GroupVersionKind) bool {
	if gvk.Group != "" {
		return false
	}
	switch strings.TrimSuffix(gvk.Kind, "List") {
	case "Secret", "ConfigMap":
		return true
	}
	return false
}

// NewGuardedClient returns a client that refuses every request against a
// Secret or ConfigMap before it is sent, and delegates everything else to c.
// Server-side apply is refused entirely because the server never uses it.
func NewGuardedClient(c ctrlclient.Client) ctrlclient.Client {
	return &guardedClient{Client: c}
}

type guardedClient struct {
	ctrlclient.Client
}

func check(c ctrlclient.Client, obj runtime.Object) error {
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Kind == "" {
		var err error
		if gvk, err = c.GroupVersionKindFor(obj); err != nil {
			// Refuse objects whose kind cannot be determined.
			return fmt.Errorf("cannot determine kind of %T: %w", obj, err)
		}
	}
	if IsForbiddenGVK(gvk) {
		return ErrForbiddenKind
	}
	return nil
}

func (g *guardedClient) Get(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object, opts ...ctrlclient.GetOption) error {
	if err := check(g.Client, obj); err != nil {
		return err
	}
	return g.Client.Get(ctx, key, obj, opts...)
}

func (g *guardedClient) List(ctx context.Context, list ctrlclient.ObjectList, opts ...ctrlclient.ListOption) error {
	if err := check(g.Client, list); err != nil {
		return err
	}
	return g.Client.List(ctx, list, opts...)
}

func (g *guardedClient) Create(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.CreateOption) error {
	if err := check(g.Client, obj); err != nil {
		return err
	}
	return g.Client.Create(ctx, obj, opts...)
}

func (g *guardedClient) Delete(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.DeleteOption) error {
	if err := check(g.Client, obj); err != nil {
		return err
	}
	return g.Client.Delete(ctx, obj, opts...)
}

func (g *guardedClient) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.UpdateOption) error {
	if err := check(g.Client, obj); err != nil {
		return err
	}
	return g.Client.Update(ctx, obj, opts...)
}

func (g *guardedClient) Patch(ctx context.Context, obj ctrlclient.Object, patch ctrlclient.Patch, opts ...ctrlclient.PatchOption) error {
	if err := check(g.Client, obj); err != nil {
		return err
	}
	return g.Client.Patch(ctx, obj, patch, opts...)
}

func (g *guardedClient) DeleteAllOf(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.DeleteAllOfOption) error {
	if err := check(g.Client, obj); err != nil {
		return err
	}
	return g.Client.DeleteAllOf(ctx, obj, opts...)
}

func (g *guardedClient) Apply(context.Context, runtime.ApplyConfiguration, ...ctrlclient.ApplyOption) error {
	return fmt.Errorf("vmop-mcp does not use server-side apply")
}

func (g *guardedClient) Status() ctrlclient.SubResourceWriter {
	return &guardedSubResource{parent: g.Client, inner: g.Client.SubResource("status")}
}

func (g *guardedClient) SubResource(subResource string) ctrlclient.SubResourceClient {
	return &guardedSubResource{parent: g.Client, inner: g.Client.SubResource(subResource)}
}

type guardedSubResource struct {
	parent ctrlclient.Client
	inner  ctrlclient.SubResourceClient
}

func (g *guardedSubResource) Get(ctx context.Context, obj ctrlclient.Object, sub ctrlclient.Object, opts ...ctrlclient.SubResourceGetOption) error {
	if err := check(g.parent, obj); err != nil {
		return err
	}
	return g.inner.Get(ctx, obj, sub, opts...)
}

func (g *guardedSubResource) Create(ctx context.Context, obj ctrlclient.Object, sub ctrlclient.Object, opts ...ctrlclient.SubResourceCreateOption) error {
	if err := check(g.parent, obj); err != nil {
		return err
	}
	return g.inner.Create(ctx, obj, sub, opts...)
}

func (g *guardedSubResource) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	if err := check(g.parent, obj); err != nil {
		return err
	}
	return g.inner.Update(ctx, obj, opts...)
}

func (g *guardedSubResource) Patch(ctx context.Context, obj ctrlclient.Object, patch ctrlclient.Patch, opts ...ctrlclient.SubResourcePatchOption) error {
	if err := check(g.parent, obj); err != nil {
		return err
	}
	return g.inner.Patch(ctx, obj, patch, opts...)
}

func (g *guardedSubResource) Apply(context.Context, runtime.ApplyConfiguration, ...ctrlclient.SubResourceApplyOption) error {
	return fmt.Errorf("vmop-mcp does not use server-side apply")
}
