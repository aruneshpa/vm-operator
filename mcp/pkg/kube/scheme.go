// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package kube builds the Kubernetes clients used by vmop-mcp from the user's
// kubeconfig, reloads them when credentials change, and guards them so that
// no request is ever issued against Secrets or ConfigMaps.
package kube

import (
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	capv1 "github.com/vmware-tanzu/vm-operator/external/capabilities/api/v1alpha1"
)

// VMOperatorGroup is the VM Operator API group.
const VMOperatorGroup = "vmoperator.vmware.com"

// NewScheme returns the scheme used by vmop-mcp clients.
func NewScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		authenticationv1.AddToScheme,
		authorizationv1.AddToScheme,
		vmopv1.AddToScheme,
		capv1.AddToScheme,
	} {
		if err := add(s); err != nil {
			panic(err)
		}
	}
	return s
}

// NewRESTMapper returns a static REST mapper for exactly the kinds vmop-mcp
// uses. Using a static mapper means rebuilding a client after a credential
// change never needs discovery, and a kind outside this list, such as Secret,
// cannot be resolved at all.
func NewRESTMapper() meta.RESTMapper {
	vmop := vmopv1.GroupVersion
	core := corev1.SchemeGroupVersion
	m := meta.NewDefaultRESTMapper([]schema.GroupVersion{vmop, core, capv1.GroupVersion})
	for _, k := range []string{
		"VirtualMachine",
		"VirtualMachineClass",
		"VirtualMachineImage",
		"VirtualMachineSnapshot",
		"VirtualMachineGroup",
		"VirtualMachineReplicaSet",
	} {
		m.Add(vmop.WithKind(k), meta.RESTScopeNamespace)
	}
	m.Add(vmop.WithKind("ClusterVirtualMachineImage"), meta.RESTScopeRoot)
	for _, k := range []string{"Event", "PersistentVolumeClaim", "ResourceQuota"} {
		m.Add(core.WithKind(k), meta.RESTScopeNamespace)
	}
	m.AddSpecific(
		capv1.GroupVersion.WithKind("Capabilities"),
		capv1.GroupVersion.WithResource("capabilities"),
		capv1.GroupVersion.WithResource("capabilities"),
		meta.RESTScopeRoot)
	return m
}
