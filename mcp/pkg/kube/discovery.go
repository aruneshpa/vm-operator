// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"fmt"
	"slices"
	"strings"

	"k8s.io/client-go/discovery"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
)

// ServedVMOperatorVersions returns the versions of the VM Operator API group
// served by the API server.
func ServedVMOperatorVersions(dc discovery.DiscoveryInterface) ([]string, error) {
	groups, err := dc.ServerGroups()
	if err != nil {
		return nil, fmt.Errorf("failed to discover API groups: %w", err)
	}
	var versions []string
	for _, g := range groups.Groups {
		if g.Name != VMOperatorGroup {
			continue
		}
		for _, v := range g.Versions {
			versions = append(versions, v.Version)
		}
	}
	slices.Sort(versions)
	return versions, nil
}

// CheckServed returns an error unless versions contains the API version this
// server is built for.
func CheckServed(versions []string) error {
	if slices.Contains(versions, contract.BuiltForAPIVersion) {
		return nil
	}
	served := "none"
	if len(versions) > 0 {
		served = strings.Join(versions, ", ")
	}
	return fmt.Errorf(
		"this vmop-mcp is built for %s/%s, but the Supervisor serves: %s; "+
			"use a vmop-mcp release that matches this Supervisor",
		VMOperatorGroup, contract.BuiltForAPIVersion, served)
}
