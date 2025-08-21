// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package util_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware-tanzu/vm-operator/pkg/util"
)

var _ = DescribeTable("MapToSlice",
	func(in map[string]string, f func(k, v string) string, out []string) {
		Expect(util.MapToSlice(in, f)).To(ContainElements(out))
	},
	Entry("empty map", nil, nil, nil),
	Entry(
		"map with key-value pairs",
		map[string]string{
			"k1": "v1",
			"k2": "v2",
		},
		func(k, v string) string {
			return fmt.Sprintf("%v:%v", k, v)
		},
		[]string{"k1:v1", "k2:v2"}, // Sorted values
	),
)
