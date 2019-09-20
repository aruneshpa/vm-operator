// +build !integration

// Copyright (c) 2019 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package vsphere_test

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware-tanzu/vm-operator/pkg/vmprovider/providers/vsphere"
)

var _ = Describe("GetResourcePool", func() {

	Context("RP as inventory path", func() {
		Specify("returns RP object without error", func() {
			res := simulator.VPX().Run(func(ctx context.Context, c *vim25.Client) error {
				finder := find.NewFinder(c)
				pools, err := finder.ResourcePoolList(ctx, "*")
				Expect(err).ShouldNot(HaveOccurred())
				Expect(len(pools)).ToNot(BeZero())

				paths := []string{
					pools[0].InventoryPath,
					pools[0].Reference().Value,
				}

				for _, path := range paths {
					pool, err := vsphere.GetResourcePool(ctx, finder, path)
					Expect(err).ShouldNot(HaveOccurred())
					Expect(pool.InventoryPath).To(Equal(pools[0].InventoryPath))
				}
				return nil
			})
			Expect(res).To(BeNil())
		})
	})

	Context("Folder as inventory path", func() {
		Specify("returns Folder object without error", func() {
			res := simulator.VPX().Run(func(ctx context.Context, c *vim25.Client) error {
				finder := find.NewFinder(c)
				folders, err := finder.FolderList(ctx, "*")
				Expect(err).ShouldNot(HaveOccurred())
				Expect(len(folders)).ToNot(BeZero())

				paths := []string{
					folders[0].InventoryPath,
					folders[0].Reference().Value,
				}

				for _, path := range paths {
					folder, err := vsphere.GetVMFolder(ctx, finder, path)
					Expect(err).ShouldNot(HaveOccurred())
					Expect(folder.InventoryPath).To(Equal(folders[0].InventoryPath))
				}

				return nil
			})
			Expect(res).To(BeNil())
		})
	})
})