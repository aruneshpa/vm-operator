// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/labels"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
)

// Limits.
const (
	// MaxResultBytes caps the serialized structured content of any result.
	MaxResultBytes = 64 << 10

	// DefaultListLimit is the default page size.
	DefaultListLimit = 50

	// MaxListLimit is the maximum page size.
	MaxListLimit = 200

	// listBudget leaves room for the page envelope.
	listBudget = MaxResultBytes - 2048
)

// ListLimit returns the effective page size for a requested limit.
func ListLimit(limit int) int64 {
	switch {
	case limit <= 0:
		return DefaultListLimit
	case limit > MaxListLimit:
		return MaxListLimit
	default:
		return int64(limit)
	}
}

// ListOptions builds list options for a namespace (empty for cluster-scoped
// kinds), label selector, limit, and continue token.
func ListOptions(ns string, in contract.ListInput, token string) ([]ctrlclient.ListOption, error) {
	opts := []ctrlclient.ListOption{ctrlclient.Limit(ListLimit(in.Limit))}
	if ns != "" {
		opts = append(opts, ctrlclient.InNamespace(ns))
	}
	if in.LabelSelector != "" {
		sel, err := labels.Parse(in.LabelSelector)
		if err != nil {
			return nil, contract.NewError(contract.CodeInvalid, "invalid labelSelector: "+err.Error(), "")
		}
		opts = append(opts, ctrlclient.MatchingLabelsSelector{Selector: sel})
	}
	if token != "" {
		opts = append(opts, ctrlclient.Continue(token))
	}
	return opts, nil
}

// Fit returns the longest prefix of items whose serialized size fits within
// the list budget, and whether any items were dropped.
func Fit[T any](items []T) ([]T, bool) {
	size := 0
	for i, it := range items {
		b, err := json.Marshal(it)
		if err != nil {
			return items[:i], true
		}
		size += len(b) + 1
		if size > listBudget {
			return items[:i], true
		}
	}
	return items, false
}

// WritePage appends paging hints to a text summary.
func WritePage(b *strings.Builder, p contract.Page) {
	if p.NextCursor != "" {
		b.WriteString("\nmore results: pass cursor=" + p.NextCursor)
	}
	if p.Truncated {
		b.WriteString("\ntruncated: " + p.Hint)
	}
}

// TruncationHint returns the hint used when Fit drops items.
func TruncationHint(kept int) string {
	return fmt.Sprintf("result exceeded the size limit; retry with limit=%d", kept)
}
