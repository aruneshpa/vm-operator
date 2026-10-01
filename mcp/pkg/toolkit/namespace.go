// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"fmt"
	"slices"
	"strings"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
)

// NamespacePolicy resolves the namespace of a request and enforces the
// optional allow-list. Cluster-scoped kinds are never subject to it.
type NamespacePolicy struct {
	// Default is used when a request does not name a namespace.
	Default string

	allowed []string
}

// NewNamespacePolicy returns a policy. An empty allowed list permits every
// namespace.
func NewNamespacePolicy(defaultNS string, allowed []string) *NamespacePolicy {
	var cleaned []string
	for _, ns := range allowed {
		if ns = strings.TrimSpace(ns); ns != "" {
			cleaned = append(cleaned, ns)
		}
	}
	slices.Sort(cleaned)
	return &NamespacePolicy{Default: defaultNS, allowed: slices.Compact(cleaned)}
}

// Allowed returns the allow-list, which is empty when unrestricted.
func (p *NamespacePolicy) Allowed() []string {
	return append([]string(nil), p.allowed...)
}

// Resolve returns the effective namespace for ns, or a ToolError if none can
// be determined or the namespace is not allowed. No API call is made.
func (p *NamespacePolicy) Resolve(ns string) (string, error) {
	if ns == "" {
		ns = p.Default
	}
	if ns == "" {
		return "", contract.NewError(contract.CodeInvalid,
			"no namespace given and the kubeconfig context has no default namespace",
			"pass namespace explicitly")
	}
	if len(p.allowed) > 0 && !slices.Contains(p.allowed, ns) {
		return "", contract.NewError(contract.CodeNamespaceNotAllowed,
			fmt.Sprintf("namespace %q is not in the server's allow-list", ns),
			"allowed namespaces: "+strings.Join(p.allowed, ", "))
	}
	return ns, nil
}
