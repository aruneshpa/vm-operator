// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package util

// MapToSlice returns a sorted slice that is formed by appending the
// entries of the given map. The map entries are marshaled to strings
// using the provided method.
func MapToSlice[K comparable, V any](m map[K]V, f func(K, V) string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, f(k, v))
	}

	return out
}
