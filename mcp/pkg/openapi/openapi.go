// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package openapi explains individual VM Service API fields from the
// Supervisor's OpenAPI v3 document, so answers always match the API version
// the Supervisor serves.
package openapi

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/kube-openapi/pkg/spec3"
	"k8s.io/kube-openapi/pkg/validation/spec"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
)

// Kinds are the kinds explain_field accepts.
var Kinds = []string{
	"VirtualMachine",
	"VirtualMachineClass",
	"VirtualMachineImage",
	"ClusterVirtualMachineImage",
	"VirtualMachineSnapshot",
	"VirtualMachineGroup",
	"VirtualMachineReplicaSet",
	"VirtualMachineService",
	"VirtualMachinePublishRequest",
	"VirtualMachineWebConsoleRequest",
	"VirtualMachineSetResourcePolicy",
}

// Fetcher returns the OpenAPI v3 document for the VM Operator group-version.
type Fetcher func() (*spec3.OpenAPI, error)

// Explainer caches the document for the lifetime of the process.
type Explainer struct {
	fetch Fetcher

	mu  sync.Mutex
	doc *spec3.OpenAPI
}

// NewExplainer returns an Explainer that uses fetch on first use.
func NewExplainer(fetch Fetcher) *Explainer {
	return &Explainer{fetch: fetch}
}

func (e *Explainer) document() (*spec3.OpenAPI, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.doc != nil {
		return e.doc, nil
	}
	doc, err := e.fetch()
	if err != nil {
		if apierrors.IsForbidden(err) || apierrors.IsNotFound(err) || apierrors.IsUnauthorized(err) {
			return nil, contract.NewError(contract.CodeSchemaUnavailable,
				"the Supervisor's API schema is not readable: "+err.Error(), "")
		}
		return nil, contract.NewError(contract.CodeSchemaUnavailable, "failed to fetch the API schema: "+err.Error(), "")
	}
	if doc == nil || doc.Components == nil {
		return nil, contract.NewError(contract.CodeSchemaUnavailable, "the API schema has no components", "")
	}
	e.doc = doc
	return doc, nil
}

// Explain returns the explanation of fieldPath within kind. An empty
// fieldPath explains the kind's root.
func (e *Explainer) Explain(kind, fieldPath string) (contract.Explanation, error) {
	out := contract.Explanation{Kind: kind, APIVersion: vmopv1.GroupVersion.String(), FieldPath: fieldPath}
	if !slices.Contains(Kinds, kind) {
		return out, contract.NewError(contract.CodeInvalid, fmt.Sprintf("unsupported kind %q", kind),
			"supported kinds: "+strings.Join(Kinds, ", "))
	}
	doc, err := e.document()
	if err != nil {
		return out, err
	}
	root := findKind(doc, kind)
	if root == nil {
		return out, contract.NewError(contract.CodeNotFound,
			fmt.Sprintf("kind %s is not in the Supervisor's %s schema", kind, vmopv1.GroupVersion), "")
	}

	cur := resolve(doc, root)
	var parent *spec.Schema
	var name string
	if fieldPath != "" {
		for _, seg := range strings.Split(fieldPath, ".") {
			next := child(doc, cur, seg)
			if next == nil {
				return out, contract.NewError(contract.CodeNotFound,
					fmt.Sprintf("field %q not found in %s", fieldPath, kind), "explain the parent path to list its fields")
			}
			parent, name, cur = cur, seg, next
		}
	}

	out.Type = typeOf(doc, cur)
	out.Description = cur.Description
	for _, v := range cur.Enum {
		out.Enum = append(out.Enum, fmt.Sprint(v))
	}
	if cur.Default != nil {
		if b, err := json.Marshal(cur.Default); err == nil {
			out.Default = strings.Trim(string(b), `"`)
		}
	}
	if parent != nil {
		out.Required = slices.Contains(parent.Required, name)
	}
	props := objectOf(doc, cur)
	if props != nil {
		names := make([]string, 0, len(props.Properties))
		for n := range props.Properties {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			s := props.Properties[n]
			rs := resolve(doc, &s)
			out.Children = append(out.Children, contract.FieldChild{
				Name:        n,
				Type:        typeOf(doc, rs),
				Description: firstSentence(rs.Description),
			})
		}
	}
	return out, nil
}

func findKind(doc *spec3.OpenAPI, kind string) *spec.Schema {
	for _, s := range doc.Components.Schemas {
		raw, ok := s.Extensions["x-kubernetes-group-version-kind"]
		if !ok {
			continue
		}
		b, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		var gvks []struct{ Group, Version, Kind string }
		if err := json.Unmarshal(b, &gvks); err != nil {
			continue
		}
		for _, g := range gvks {
			if g.Group == vmopv1.GroupVersion.Group && g.Version == vmopv1.GroupVersion.Version && g.Kind == kind {
				return s
			}
		}
	}
	return nil
}

// resolve follows $ref and single-element allOf wrappers. Kubernetes
// publishes a described reference as an allOf wrapper around a $ref, so the
// first description seen along the way is kept when the target has none.
func resolve(doc *spec3.OpenAPI, s *spec.Schema) *spec.Schema {
	desc := ""
	for range 16 {
		if desc == "" {
			desc = s.Description
		}
		if ref := s.Ref.String(); ref != "" {
			name := strings.TrimPrefix(ref, "#/components/schemas/")
			target, ok := doc.Components.Schemas[name]
			if !ok {
				break
			}
			s = target
			continue
		}
		if len(s.AllOf) == 1 && len(s.Properties) == 0 && len(s.Type) == 0 {
			s = &s.AllOf[0]
			continue
		}
		break
	}
	if s.Description == "" && desc != "" {
		c := *s
		c.Description = desc
		return &c
	}
	return s
}

// objectOf returns the schema whose properties describe s's children: s
// itself for objects, or the item schema for arrays of objects.
func objectOf(doc *spec3.OpenAPI, s *spec.Schema) *spec.Schema {
	if len(s.Properties) > 0 {
		return s
	}
	if s.Items != nil && s.Items.Schema != nil {
		item := resolve(doc, s.Items.Schema)
		if len(item.Properties) > 0 {
			return item
		}
	}
	return nil
}

func child(doc *spec3.OpenAPI, s *spec.Schema, name string) *spec.Schema {
	obj := objectOf(doc, s)
	if obj == nil {
		return nil
	}
	c, ok := obj.Properties[name]
	if !ok {
		return nil
	}
	return resolve(doc, &c)
}

func typeOf(doc *spec3.OpenAPI, s *spec.Schema) string {
	if len(s.Type) == 0 {
		if len(s.Properties) > 0 {
			return "object"
		}
		return ""
	}
	t := s.Type[0]
	if t == "array" && s.Items != nil && s.Items.Schema != nil {
		return "[]" + typeOf(doc, resolve(doc, s.Items.Schema))
	}
	return t
}

func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i+1]
	}
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
