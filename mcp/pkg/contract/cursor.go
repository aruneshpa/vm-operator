// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package contract

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Cursor is the decoded form of an opaque list cursor. Phase distinguishes
// the sub-lists of a merged list, such as namespaced and cluster-scoped
// images; Token is the upstream Kubernetes continue token.
type Cursor struct {
	V     int    `json:"v"`
	Phase string `json:"k,omitempty"`
	Token string `json:"t,omitempty"`
}

// EncodeCursor returns the opaque form of a cursor.
func EncodeCursor(phase, token string) string {
	b, err := json.Marshal(Cursor{V: 1, Phase: phase, Token: token})
	if err != nil {
		// A struct of strings always marshals.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor parses an opaque cursor. An empty string decodes to the zero
// Cursor.
func DecodeCursor(s string) (Cursor, error) {
	var c Cursor
	if s == "" {
		return c, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return c, NewError(CodeInvalid, fmt.Sprintf("malformed cursor: %v", err), "restart listing without cursor")
	}
	if err := json.Unmarshal(b, &c); err != nil || c.V != 1 {
		return Cursor{}, NewError(CodeInvalid, "malformed cursor", "restart listing without cursor")
	}
	return c, nil
}
