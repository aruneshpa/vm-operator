// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package capabilities detects which feature-gated VM Service capabilities
// are active on the connected Supervisor.
package capabilities

import (
	"context"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	capv1 "github.com/vmware-tanzu/vm-operator/external/capabilities/api/v1alpha1"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/toolkit"
)

const (
	// SupervisorCapabilitiesName is the name of the cluster-scoped
	// Capabilities object. It matches the name VM Operator reads.
	SupervisorCapabilitiesName = "supervisor-capabilities"

	// KeyVMSnapshots is the Supervisor capability key for VM snapshots. It
	// matches the key VM Operator uses to enable the VMSnapshots feature.
	KeyVMSnapshots = "supports_VM_service_VM_snapshots"
)

// keys maps a vmop-mcp capability name to its Supervisor capability key.
var keys = map[string]capv1.CapabilityName{
	toolkit.CapabilityVMSnapshots: KeyVMSnapshots,
}

// Detect returns the state of every known capability. If the Capabilities
// object cannot be read, for example because the user is not permitted to
// read it, every capability is reported as unknown.
func Detect(ctx context.Context, c ctrlclient.Client) map[string]string {
	out := make(map[string]string, len(keys))
	for name := range keys {
		out[name] = toolkit.CapabilityUnknown
	}

	var obj capv1.Capabilities
	if err := c.Get(ctx, ctrlclient.ObjectKey{Name: SupervisorCapabilitiesName}, &obj); err != nil {
		return out
	}
	for name, key := range keys {
		st, ok := obj.Status.Supervisor[key]
		switch {
		case !ok:
			out[name] = toolkit.CapabilityInactive
		case st.Activated:
			out[name] = toolkit.CapabilityActive
		default:
			out[name] = toolkit.CapabilityInactive
		}
	}
	return out
}
