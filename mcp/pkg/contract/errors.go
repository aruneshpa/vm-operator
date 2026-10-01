// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package contract

import (
	"encoding/json"
	"errors"
)

// Tool error codes.
const (
	CodeUnauthorized        = "unauthorized"
	CodeForbidden           = "forbidden"
	CodeNotFound            = "not_found"
	CodeConflict            = "conflict"
	CodeInvalid             = "invalid"
	CodeNamespaceNotAllowed = "namespace_not_allowed"
	CodeCursorExpired       = "cursor_expired"
	CodePreconditionFailed  = "precondition_failed"
	CodeSchemaUnavailable   = "schema_unavailable"
	CodeTimeout             = "timeout"
	CodeInternal            = "internal"
)

// ToolError is a tool-level failure. It is returned to the client as the JSON
// text content of a result whose isError field is true.
type ToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// Error returns the JSON encoding of the error, which is what clients see.
func (e *ToolError) Error() string {
	b, err := json.Marshal(e)
	if err != nil {
		return e.Code + ": " + e.Message
	}
	return string(b)
}

// NewError returns a new ToolError.
func NewError(code, message, hint string) *ToolError {
	return &ToolError{Code: code, Message: message, Hint: hint}
}

// CodeOf returns the code of err if it is a ToolError, otherwise "".
func CodeOf(err error) string {
	if te, ok := errors.AsType[*ToolError](err); ok {
		return te.Code
	}
	return ""
}
