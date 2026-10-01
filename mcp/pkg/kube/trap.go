// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"context"
	"sync"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// SecretTrap records every request against a Secret or ConfigMap that reaches
// the underlying client. It is intended for tests: install Funcs on a fake
// client *beneath* the guarded client and assert that Violations is empty.
type SecretTrap struct {
	mu         sync.Mutex
	violations []string
}

// NewSecretTrap returns a new SecretTrap.
func NewSecretTrap() *SecretTrap {
	return &SecretTrap{}
}

// Violations returns the recorded violations.
func (t *SecretTrap) Violations() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.violations...)
}

func (t *SecretTrap) record(c ctrlclient.WithWatch, verb string, obj runtime.Object) {
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Kind == "" {
		gvk, _ = c.GroupVersionKindFor(obj)
	}
	if IsForbiddenGVK(gvk) {
		t.mu.Lock()
		t.violations = append(t.violations, verb+" "+gvk.Kind)
		t.mu.Unlock()
	}
}

// Funcs returns interceptor functions that record violations and then
// delegate to the intercepted client.
func (t *SecretTrap) Funcs() interceptor.Funcs {
	return interceptor.Funcs{
		Get: func(ctx context.Context, c ctrlclient.WithWatch, key ctrlclient.ObjectKey, obj ctrlclient.Object, opts ...ctrlclient.GetOption) error {
			t.record(c, "get", obj)
			return c.Get(ctx, key, obj, opts...)
		},
		List: func(ctx context.Context, c ctrlclient.WithWatch, list ctrlclient.ObjectList, opts ...ctrlclient.ListOption) error {
			t.record(c, "list", list)
			return c.List(ctx, list, opts...)
		},
		Create: func(ctx context.Context, c ctrlclient.WithWatch, obj ctrlclient.Object, opts ...ctrlclient.CreateOption) error {
			t.record(c, "create", obj)
			return c.Create(ctx, obj, opts...)
		},
		Update: func(ctx context.Context, c ctrlclient.WithWatch, obj ctrlclient.Object, opts ...ctrlclient.UpdateOption) error {
			t.record(c, "update", obj)
			return c.Update(ctx, obj, opts...)
		},
		Patch: func(ctx context.Context, c ctrlclient.WithWatch, obj ctrlclient.Object, patch ctrlclient.Patch, opts ...ctrlclient.PatchOption) error {
			t.record(c, "patch", obj)
			return c.Patch(ctx, obj, patch, opts...)
		},
		Delete: func(ctx context.Context, c ctrlclient.WithWatch, obj ctrlclient.Object, opts ...ctrlclient.DeleteOption) error {
			t.record(c, "delete", obj)
			return c.Delete(ctx, obj, opts...)
		},
		Watch: func(ctx context.Context, c ctrlclient.WithWatch, list ctrlclient.ObjectList, opts ...ctrlclient.ListOption) (watch.Interface, error) {
			t.record(c, "watch", list)
			return c.Watch(ctx, list, opts...)
		},
	}
}
