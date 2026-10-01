// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package explain implements the explain_field tool.
package explain

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/client-go/openapi3"
	"k8s.io/kube-openapi/pkg/spec3"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/kube"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/openapi"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/toolkit"
)

// Register registers explain_field. If explainer is nil, one backed by the
// Supervisor's /openapi/v3 endpoint is created.
func Register(r *toolkit.Registrar, explainer *openapi.Explainer) {
	env := r.Env
	if explainer == nil {
		explainer = openapi.NewExplainer(func() (*spec3.OpenAPI, error) {
			return kube.Do(context.Background(), env.Provider, func(c *kube.Clients) (*spec3.OpenAPI, error) {
				return openapi3.NewRoot(c.Kube.Discovery().OpenAPIV3()).GVSpec(vmopv1.GroupVersion)
			})
		})
	}

	toolkit.AddRead(r, toolkit.Spec[contract.Explanation]{
		Name:  "explain_field",
		Title: "Explain API field",
		Description: "Explains one field of a VM Service API kind, like kubectl explain: description, type, allowed " +
			"values, default, whether it is required, and its direct children. Uses the Supervisor's own API schema.",
		Enums: map[string][]string{"kind": openapi.Kinds},
		Summary: func(e contract.Explanation) string {
			var b strings.Builder
			fmt.Fprintf(&b, "%s %s (%s)", e.Kind, e.FieldPath, e.Type)
			if len(e.Enum) > 0 {
				b.WriteString(" enum=" + strings.Join(e.Enum, "|"))
			}
			if e.Default != "" {
				b.WriteString(" default=" + e.Default)
			}
			if e.Description != "" {
				b.WriteString("\n" + e.Description)
			}
			for _, c := range e.Children {
				fmt.Fprintf(&b, "\n  %s (%s): %s", c.Name, c.Type, c.Description)
			}
			return b.String()
		},
	}, func(_ context.Context, in contract.ExplainInput) (contract.Explanation, error) {
		return explainer.Explain(in.Kind, in.FieldPath)
	})
}
