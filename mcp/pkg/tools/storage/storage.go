// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package storage implements the list_storage_classes tool.
package storage

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/kube"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/toolkit"
)

// quotaSuffix is the ResourceQuota key suffix that assigns a storage class to
// a namespace.
const quotaSuffix = ".storageclass.storage.k8s.io/requests.storage"

// Register registers list_storage_classes. It is a read-tier tool.
func Register(r *toolkit.Registrar) {
	env := r.Env
	toolkit.AddRead(r, toolkit.Spec[contract.StorageClasses]{
		Name:  "list_storage_classes",
		Title: "List storage classes",
		Description: "Lists the storage classes assigned to a namespace, with quota limit and usage, " +
			"from the namespace's ResourceQuotas.",
		Summary: func(s contract.StorageClasses) string {
			var b strings.Builder
			fmt.Fprintf(&b, "%d storage classes", len(s.StorageClasses))
			for _, sc := range s.StorageClasses {
				fmt.Fprintf(&b, "\n%s limit=%s used=%s", sc.Name, sc.QuotaLimit, sc.QuotaUsed)
			}
			return b.String()
		},
	}, func(ctx context.Context, in contract.StorageClassesInput) (contract.StorageClasses, error) {
		ns, err := env.Namespaces.Resolve(in.Namespace)
		if err != nil {
			return contract.StorageClasses{}, err
		}
		return List(ctx, env, ns)
	})
}

// List returns the storage classes assigned to an already-resolved namespace.
func List(ctx context.Context, env *toolkit.Env, ns string) (contract.StorageClasses, error) {
	out := contract.StorageClasses{StorageClasses: []contract.StorageClassQuota{}}
	l, err := kube.Do(ctx, env.Provider, func(c *kube.Clients) (*corev1.ResourceQuotaList, error) {
		l := &corev1.ResourceQuotaList{}
		return l, c.Client.List(ctx, l, ctrlclient.InNamespace(ns))
	})
	if err != nil {
		return out, err
	}
	byName := map[string]*contract.StorageClassQuota{}
	for _, rq := range l.Items {
		for res, q := range rq.Spec.Hard {
			name, ok := strings.CutSuffix(string(res), quotaSuffix)
			if !ok {
				continue
			}
			sc := byName[name]
			if sc == nil {
				sc = &contract.StorageClassQuota{Name: name}
				byName[name] = sc
			}
			sc.QuotaLimit = q.String()
			if used, ok := rq.Status.Used[res]; ok {
				sc.QuotaUsed = used.String()
			}
		}
	}
	for _, sc := range byName {
		out.StorageClasses = append(out.StorageClasses, *sc)
	}
	sort.Slice(out.StorageClasses, func(i, j int) bool { return out.StorageClasses[i].Name < out.StorageClasses[j].Name })
	return out, nil
}
