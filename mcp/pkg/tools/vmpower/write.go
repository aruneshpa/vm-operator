// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package vmpower

import (
	"context"
	"fmt"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/kube"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/toolkit"
)

// FieldOwner is the field manager recorded for every write.
const FieldOwner = "vmop-mcp"

// MaxAttempts bounds retries after an optimistic-lock conflict.
const MaxAttempts = 3

// Mutation changes vm in place. It is called with a fresh copy on every
// attempt, so it must re-evaluate its preconditions each time.
type Mutation func(vm *vmopv1.VirtualMachine) error

// PatchVM reads the VM, applies mutate, and patches the difference with a
// resourceVersion precondition. If the spec is unchanged, no request is sent
// and changed is false. On a conflict, the whole read-mutate-patch cycle is
// retried up to MaxAttempts times, so a concurrent change is never
// overwritten. The returned VM is the object as the API server returned it,
// i.e. after mutating admission.
func PatchVM(
	ctx context.Context,
	env *toolkit.Env,
	ns, name string,
	dryRun bool,
	mutate Mutation) (*vmopv1.VirtualMachine, bool, error) {

	opts := []ctrlclient.PatchOption{ctrlclient.FieldOwner(FieldOwner)}
	if dryRun {
		opts = append(opts, ctrlclient.DryRunAll)
	}

	type result struct {
		vm      *vmopv1.VirtualMachine
		changed bool
	}
	for range MaxAttempts {
		res, err := kube.Do(ctx, env.Provider, func(c *kube.Clients) (result, error) {
			obj := &vmopv1.VirtualMachine{}
			if err := c.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: ns, Name: name}, obj); err != nil {
				return result{}, err
			}
			base := obj.DeepCopy()
			if err := mutate(obj); err != nil {
				return result{}, err
			}
			if apiequality.Semantic.DeepEqual(base.Spec, obj.Spec) {
				return result{vm: obj}, nil
			}
			patch := ctrlclient.MergeFromWithOptions(base, ctrlclient.MergeFromWithOptimisticLock{})
			if err := c.Client.Patch(ctx, obj, patch, opts...); err != nil {
				return result{}, err
			}
			return result{vm: obj, changed: true}, nil
		})
		if !apierrors.IsConflict(err) {
			return res.vm, res.changed, err
		}
	}
	return nil, false, contract.NewError(contract.CodeConflict,
		fmt.Sprintf("VirtualMachine %s/%s kept changing concurrently; gave up after %d attempts", ns, name, MaxAttempts),
		"retry later")
}
