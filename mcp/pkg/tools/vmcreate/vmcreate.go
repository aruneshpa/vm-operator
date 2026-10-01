// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package vmcreate implements the write-tier create_virtual_machine tool.
package vmcreate

import (
	"context"
	"fmt"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	vmopv1common "github.com/vmware-tanzu/vm-operator/api/v1alpha6/common"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	engine "github.com/vmware-tanzu/vm-operator/mcp/pkg/diagnose"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/kube"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/projection"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/toolkit"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/tools/diagnose"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/tools/storage"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/tools/vmpower"
)

// Bootstrap providers accepted by create_virtual_machine.
const (
	ProviderCloudInit = "cloudInit"
	ProviderSysprep   = "sysprep"

	defaultCloudInitKey = "user-data"
	defaultSysprepKey   = "unattend"
)

// Register registers create_virtual_machine. It is a write-tier tool.
func Register(r *toolkit.Registrar) {
	env := r.Env
	toolkit.AddWrite(r, toolkit.Spec[contract.CreateVirtualMachineOutput]{
		Name:  "create_virtual_machine",
		Title: "Create VM",
		Description: "Creates a VirtualMachine from a class, an image, and a storage class, optionally with network " +
			"names and a reference to an existing bootstrap Secret. Always call first with dryRun=true: this runs " +
			"preflight checks and Supervisor admission and returns the VM as it would be admitted, without creating " +
			"it. Create for real only after the user confirms.",
		Enums: map[string][]string{
			"powerState": {string(vmopv1.VirtualMachinePowerStateOn), string(vmopv1.VirtualMachinePowerStateOff)},
		},
		Summary: func(o contract.CreateVirtualMachineOutput) string {
			var b strings.Builder
			fmt.Fprintf(&b, "admitted=%t dryRun=%t", o.Admitted, o.DryRun)
			for _, f := range o.Preflight {
				fmt.Fprintf(&b, "\n[%s] %s: %s", f.Severity, f.Check, f.Message)
			}
			if o.VM != nil {
				fmt.Fprintf(&b, "\nvm %s/%s class=%s image=%s/%s storageClass=%s power=%s",
					o.VM.Namespace, o.VM.Name, o.VM.ClassName, o.VM.Image.Kind, o.VM.Image.Name,
					o.VM.StorageClass, o.VM.PowerState.Desired)
			}
			return b.String()
		},
	}, func(ctx context.Context, in contract.CreateVirtualMachineInput) (contract.CreateVirtualMachineOutput, error) {
		return create(ctx, env, in)
	})
}

func validate(in contract.CreateVirtualMachineInput) error {
	if errs := validation.IsDNS1123Subdomain(in.Name); len(errs) > 0 {
		return contract.NewError(contract.CodeInvalid, "invalid name: "+strings.Join(errs, "; "), "")
	}
	for field, v := range map[string]string{"className": in.ClassName, "imageName": in.ImageName, "storageClass": in.StorageClass} {
		if v == "" {
			return contract.NewError(contract.CodeInvalid, field+" is required", "")
		}
	}
	if b := in.Bootstrap; b != nil {
		if !slices.Contains([]string{ProviderCloudInit, ProviderSysprep}, b.Provider) {
			return contract.NewError(contract.CodeInvalid,
				fmt.Sprintf("bootstrap provider must be %s or %s", ProviderCloudInit, ProviderSysprep), "")
		}
		if b.SecretName == "" {
			return contract.NewError(contract.CodeInvalid, "bootstrap secretName is required", "")
		}
	}
	return nil
}

// Build returns the VirtualMachine described by in. Bootstrap data is only
// referenced by Secret name and key; the Secret is never read.
func Build(ns string, in contract.CreateVirtualMachineInput) *vmopv1.VirtualMachine {
	obj := &vmopv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: in.Name, Labels: in.Labels},
		Spec: vmopv1.VirtualMachineSpec{
			ClassName:    in.ClassName,
			ImageName:    in.ImageName,
			StorageClass: in.StorageClass,
			PowerState:   vmopv1.VirtualMachinePowerStateOn,
		},
	}
	if in.PowerState != "" {
		obj.Spec.PowerState = vmopv1.VirtualMachinePowerState(in.PowerState)
	}
	if len(in.NetworkInterfaces) > 0 {
		obj.Spec.Network = &vmopv1.VirtualMachineNetworkSpec{}
		for i, n := range in.NetworkInterfaces {
			obj.Spec.Network.Interfaces = append(obj.Spec.Network.Interfaces, vmopv1.VirtualMachineNetworkInterfaceSpec{
				Name:    fmt.Sprintf("eth%d", i),
				Network: &vmopv1common.PartialObjectRef{Name: n},
			})
		}
	}
	if b := in.Bootstrap; b != nil {
		switch b.Provider {
		case ProviderCloudInit:
			key := b.Key
			if key == "" {
				key = defaultCloudInitKey
			}
			obj.Spec.Bootstrap = &vmopv1.VirtualMachineBootstrapSpec{
				CloudInit: &vmopv1.VirtualMachineBootstrapCloudInitSpec{
					RawCloudConfig: &vmopv1common.SecretKeySelector{Name: b.SecretName, Key: key},
				},
			}
		case ProviderSysprep:
			key := b.Key
			if key == "" {
				key = defaultSysprepKey
			}
			obj.Spec.Bootstrap = &vmopv1.VirtualMachineBootstrapSpec{
				Sysprep: &vmopv1.VirtualMachineBootstrapSysprepSpec{
					RawSysprep: &vmopv1common.SecretKeySelector{Name: b.SecretName, Key: key},
				},
			}
		}
	}
	return obj
}

// Preflight checks what VM Operator admission does not: that the class
// exists, that the image resolves, that the storage class is assigned to the
// namespace, and that the name is free.
func Preflight(ctx context.Context, env *toolkit.Env, ns string, in contract.CreateVirtualMachineInput) []contract.Finding {
	findings := []contract.Finding{}
	vmRef := contract.ObjectRef{Kind: "VirtualMachine", Namespace: ns, Name: in.Name}

	_, err := kube.Do(ctx, env.Provider, func(c *kube.Clients) (struct{}, error) {
		return struct{}{}, c.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: ns, Name: in.Name}, &vmopv1.VirtualMachine{})
	})
	if err == nil {
		findings = append(findings, contract.Finding{
			Severity: contract.SeverityBlocking, Object: vmRef, Check: "create.name_taken",
			Message:    "A VirtualMachine with this name already exists in the namespace.",
			Suggestion: "Choose another name.",
		})
	}

	// Reuse the diagnose lookups against a VM built from the input.
	s := diagnose.Gather(ctx, env, Build(ns, in), false)
	for _, f := range engine.Diagnose(s) {
		switch f.Check {
		case "class.not_found":
			f.Suggestion = "Call list_virtual_machine_classes and choose one of the listed classes."
			findings = append(findings, f)
		case "class.unavailable", "image.not_ready":
			findings = append(findings, f)
		case "image.not_found":
			// A display name is resolved by admission, so a failed lookup
			// by object name is not conclusive.
			f.Severity = contract.SeverityWarning
			f.Check = "image.not_found_by_name"
			f.Message = "No VirtualMachineImage or ClusterVirtualMachineImage has this object name; admission " +
				"will try to resolve it as an image display name."
			f.Suggestion = "Call list_virtual_machine_images and use an object name to avoid ambiguity."
			findings = append(findings, f)
		}
	}

	sc, err := storage.List(ctx, env, ns)
	switch {
	case err != nil:
		findings = append(findings, contract.Finding{
			Severity: contract.SeverityInfo, Object: vmRef, Check: "storage.unavailable",
			Message: "The namespace's storage quota could not be read, so the storage class was not checked.",
		})
	case len(sc.StorageClasses) == 0:
		findings = append(findings, contract.Finding{
			Severity: contract.SeverityInfo, Object: vmRef, Check: "storage.unverified",
			Message: "The namespace has no storage class quota entries, so the storage class could not be verified.",
		})
	case !slices.ContainsFunc(sc.StorageClasses, func(q contract.StorageClassQuota) bool { return q.Name == in.StorageClass }):
		findings = append(findings, contract.Finding{
			Severity:   contract.SeverityBlocking,
			Object:     contract.ObjectRef{Kind: "StorageClass", Name: in.StorageClass},
			Check:      "storage.not_assigned",
			Message:    "The storage class is not assigned to the namespace.",
			Suggestion: "Call list_storage_classes and choose one of the listed storage classes.",
		})
	}
	return findings
}

func create(ctx context.Context, env *toolkit.Env, in contract.CreateVirtualMachineInput) (contract.CreateVirtualMachineOutput, error) {
	out := contract.CreateVirtualMachineOutput{DryRun: in.DryRun}
	if err := validate(in); err != nil {
		return out, err
	}
	ns, err := env.Namespaces.Resolve(in.Namespace)
	if err != nil {
		return out, err
	}

	out.Preflight = Preflight(ctx, env, ns, in)
	if !engine.Healthy(out.Preflight) {
		return out, nil
	}

	opts := []ctrlclient.CreateOption{ctrlclient.FieldOwner(vmpower.FieldOwner)}
	if in.DryRun {
		opts = append(opts, ctrlclient.DryRunAll)
	}
	obj, err := kube.Do(ctx, env.Provider, func(c *kube.Clients) (*vmopv1.VirtualMachine, error) {
		obj := Build(ns, in)
		return obj, c.Client.Create(ctx, obj, opts...)
	})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return out, contract.NewError(contract.CodeConflict, err.Error(), "choose another name")
		}
		return out, err
	}
	out.Admitted = true
	d := projection.VirtualMachineDetail(obj)
	out.VM = &d
	return out, nil
}
