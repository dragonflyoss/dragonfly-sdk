/*
 *     Copyright 2026 The Dragonfly Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2" //nolint
	. "github.com/onsi/gomega"    //nolint

	"d7y.io/dragonfly-sdk/test/e2e/util"
)

var _ = Describe("Delete", func() {
	for _, sdk := range util.SDKs {
		Context(fmt.Sprintf("10MiB file using %s sdk", sdk.Name), func() {
			var (
				testFile *util.File
				err      error
			)

			BeforeEach(func() {
				testFile, err = util.GetFileServer().GenerateFile(util.FileSize10MiB)
				Expect(err).NotTo(HaveOccurred())
				Expect(testFile).NotTo(BeNil())
			})

			AfterEach(func() {
				err = util.GetFileServer().DeleteFile(testFile)
				Expect(err).NotTo(HaveOccurred())
			})

			It("delete should remove the task from all seed peers", Label("delete", "file", sdk.Name), func() {
				url := testFile.GetDownloadURL()
				Expect(sdk.Preheat(url)).To(Succeed())

				_, err := util.GetPreheatedSeedClients(sdk, url, testFile.GetSha256())
				Expect(err).NotTo(HaveOccurred())

				Expect(sdk.Delete(url)).To(Succeed())

				seedClients, err := util.SeedClients()
				Expect(err).NotTo(HaveOccurred())
				Expect(seedClients).To(HaveLen(util.SeedClientReplicas))
				Expect(util.CheckFilesExist(util.PodExecs(seedClients), testFile.GetTaskID())).To(BeFalse())
			})
		})
	}
})

var _ = Describe("Delete Image", func() {
	image := util.BusyboxImage
	for _, sdk := range util.SDKs {
		Context(fmt.Sprintf("%s image using %s sdk", image.GetReference(), sdk.Name), func() {
			It("delete should remove the manifest and every blob from all seed peers", Label("delete", "image", sdk.Name), func() {
				Expect(sdk.PreheatImage(image.GetReference())).To(Succeed())

				for _, blob := range image.GetBlobs() {
					_, err := util.GetPreheatedSeedClients(sdk, blob.GetURL(), blob.GetSha256())
					Expect(err).NotTo(HaveOccurred())
				}

				Expect(sdk.DeleteImage(image.GetReference())).To(Succeed())

				seedClients, err := util.SeedClients()
				Expect(err).NotTo(HaveOccurred())
				Expect(seedClients).To(HaveLen(util.SeedClientReplicas))

				for _, blob := range image.GetBlobs() {
					Expect(util.CheckFilesExist(util.PodExecs(seedClients), blob.GetTaskID())).To(BeFalse())
				}
			})
		})
	}
})
