// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package projection

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
)

// maxInlinePathDepth bounds how deep the bootstrap walk descends. Past this
// depth, content is arbitrary user data (for example cloud-init runcmd
// entries), and even its keys are not reported.
const maxInlinePathDepth = 4

// Bootstrap summarizes a bootstrap spec. It never copies a value: it reports
// which providers are configured, the Secret references by name and key, and
// the paths of fields that hold inline values.
func Bootstrap(b *vmopv1.VirtualMachineBootstrapSpec) *contract.BootstrapSummary {
	if b == nil {
		return nil
	}
	s := &contract.BootstrapSummary{Providers: []string{}, Disabled: b.Disabled}
	w := &walker{inline: map[string]struct{}{}}

	add := func(name string, v any) {
		s.Providers = append(s.Providers, name)
		raw, err := json.Marshal(v)
		if err != nil {
			w.inline[name] = struct{}{}
			return
		}
		var node any
		if err := json.Unmarshal(raw, &node); err != nil {
			w.inline[name] = struct{}{}
			return
		}
		w.walk("spec.bootstrap."+name, node, 0)
	}
	if b.CloudInit != nil {
		add("cloudInit", b.CloudInit)
	}
	if b.LinuxPrep != nil {
		add("linuxPrep", b.LinuxPrep)
	}
	if b.Sysprep != nil {
		add("sysprep", b.Sysprep)
	}
	if b.VAppConfig != nil {
		add("vAppConfig", b.VAppConfig)
	}

	for p := range w.inline {
		s.InlineFieldsPresent = append(s.InlineFieldsPresent, p)
	}
	sort.Strings(s.InlineFieldsPresent)
	sort.Slice(w.refs, func(i, j int) bool { return w.refs[i].FieldPath < w.refs[j].FieldPath })
	s.SecretRefs = slices.CompactFunc(w.refs, func(a, b contract.SecretRef) bool { return a == b })
	return s
}

type walker struct {
	inline map[string]struct{}
	refs   []contract.SecretRef
}

// SelectorFields are the bootstrap JSON field names whose value is a Secret
// key selector. A {name, key} object is reported as a Secret reference only
// at one of these fields; anywhere else it is user data and is reported as an
// inline field. A test keeps this list in sync with the API types.
var SelectorFields = map[string]struct{}{
	"content":             {}, // cloud-init write_files[].content, when not inline.
	"domainAdminPassword": {},
	"from":                {},
	"hashed_passwd":       {},
	"passwd":              {},
	"password":            {},
	"productID":           {},
	"rawCloudConfig":      {},
	"rawSysprep":          {},
}

// OpaqueFields are the bootstrap JSON field names typed as raw JSON. Their
// content is arbitrary user data, so the walk never descends into them: even
// the keys of a user-authored object could be sensitive. A test keeps this
// list in sync with the API types.
var OpaqueFields = map[string]struct{}{
	"content": {},
	"runcmd":  {},
}

// isSecretSelector reports whether m, found at a field named field, is a
// Secret key selector: the field is a known selector field, and m has a
// non-empty string "name", an optional string "key", and nothing else.
func isSecretSelector(field string, m map[string]any) (name, key string, ok bool) {
	if _, known := SelectorFields[field]; !known {
		return "", "", false
	}
	if len(m) == 0 || len(m) > 2 {
		return "", "", false
	}
	for k, v := range m {
		s, isString := v.(string)
		if !isString {
			return "", "", false
		}
		switch k {
		case "name":
			name = s
		case "key":
			key = s
		default:
			return "", "", false
		}
	}
	return name, key, name != ""
}

// lastField returns the last field name of a dotted path, ignoring "[]".
func lastField(path string) string {
	path = strings.TrimRight(path, "[]")
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[i+1:]
	}
	return path
}

func (w *walker) walk(path string, node any, depth int) {
	field := lastField(path)
	if m, isMap := node.(map[string]any); isMap {
		if name, key, ok := isSecretSelector(field, m); ok {
			w.refs = append(w.refs, contract.SecretRef{Name: name, Key: key, FieldPath: path})
			return
		}
	}
	if _, opaque := OpaqueFields[field]; opaque {
		if !isEmpty(node) {
			w.inline[path] = struct{}{}
		}
		return
	}

	switch v := node.(type) {
	case map[string]any:
		if depth >= maxInlinePathDepth {
			if len(v) > 0 {
				w.inline[path] = struct{}{}
			}
			return
		}
		for k, child := range v {
			w.walk(path+"."+k, child, depth+1)
		}
	case []any:
		for _, child := range v {
			w.walk(path+"[]", child, depth+1)
		}
	case string:
		if v != "" {
			w.inline[path] = struct{}{}
		}
	case float64:
		w.inline[path] = struct{}{}
	case bool, nil:
		// Booleans and nulls are configuration switches, not payloads.
	}
}

func isEmpty(node any) bool {
	switch v := node.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}
