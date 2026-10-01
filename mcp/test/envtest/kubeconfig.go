// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"fmt"

	"k8s.io/client-go/tools/clientcmd"
)

// SetNamespace sets the namespace of the current context in a kubeconfig.
func SetNamespace(path, namespace string) error {
	cfg, err := clientcmd.LoadFromFile(path)
	if err != nil {
		return err
	}
	ctx, ok := cfg.Contexts[cfg.CurrentContext]
	if !ok {
		return fmt.Errorf("kubeconfig %s has no current context", path)
	}
	ctx.Namespace = namespace
	return clientcmd.WriteToFile(*cfg, path)
}
