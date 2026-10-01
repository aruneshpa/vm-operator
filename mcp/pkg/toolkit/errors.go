// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"
	"errors"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/kube"
)

// MapError converts an error into a ToolError. ToolErrors pass through
// unchanged; Kubernetes API errors keep the Supervisor's message verbatim.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*contract.ToolError](err); ok {
		return err
	}
	msg := err.Error()
	code, hint := contract.CodeInternal, ""
	switch {
	case errors.Is(err, kube.ErrForbiddenKind):
		code = contract.CodeForbidden
	case errors.Is(err, context.DeadlineExceeded):
		code = contract.CodeTimeout
	case apierrors.IsUnauthorized(err):
		code = contract.CodeUnauthorized
		hint = "log in again (kubectl vsphere login or vcf context) and retry"
	case strings.Contains(msg, "admission webhook"):
		code = contract.CodeInvalid
	case apierrors.IsForbidden(err):
		code = contract.CodeForbidden
		hint = "use check_access to see what you may do"
	case apierrors.IsNotFound(err):
		code = contract.CodeNotFound
	case apierrors.IsConflict(err):
		code = contract.CodeConflict
		hint = "the object changed concurrently; retry"
	case apierrors.IsResourceExpired(err) || apierrors.IsGone(err):
		code = contract.CodeCursorExpired
		hint = "restart listing without cursor"
	case apierrors.IsInvalid(err) || apierrors.IsBadRequest(err):
		code = contract.CodeInvalid
	case apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err):
		code = contract.CodeTimeout
	}
	return contract.NewError(code, msg, hint)
}
