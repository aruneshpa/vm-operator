// Copyright (c) 2024 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"strings"

	vimtypes "github.com/vmware/govmomi/vim25/types"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: CMD <SCHEMA_VERSION> <OUT_FILE_PATH>")
		os.Exit(1)
	}

	f, err := os.Create(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() {
		if err := f.Close(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}()

	fmt.Fprintf(
		f,
		format,
		os.Args[1],
		strings.Join(vimtypes.VirtualMachineGuestOsIdentifier("").Strings(), ";"),
	)
}

const format = `// Copyright (c) 2024 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

// Code generated by pkg/gen/guestosid. DO NOT EDIT.

package %[1]s

// +kubebuilder:validation:Enum=%[2]s
type VirtualMachineGuestOSIdentifier string
`
