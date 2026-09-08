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
	"net/http"

	. "github.com/onsi/ginkgo/v2" //nolint
	. "github.com/onsi/gomega"    //nolint

	"d7y.io/dragonfly-sdk/test/e2e/util"
)

// fileSizes are the sizes of the files the preheats download, by name.
var fileSizes = []struct {
	name string
	size util.FileSize
}{
	{name: "10MiB", size: util.FileSize10MiB},
	{name: "1GiB", size: util.FileSize1GiB},
}

var _ = Describe("Preheat File", func() {
	for _, sdk := range util.SDKs {
		for _, fileSize := range fileSizes {
			Context(fmt.Sprintf("%s file using %s sdk", fileSize.name, sdk.Name), func() {
				var (
					testFile *util.File
					err      error
				)

				BeforeEach(func() {
					testFile, err = util.GetFileServer().GenerateFile(fileSize.size)
					Expect(err).NotTo(HaveOccurred())
					Expect(testFile).NotTo(BeNil())
				})

				AfterEach(func() {
					err = util.GetFileServer().DeleteFile(testFile)
					Expect(err).NotTo(HaveOccurred())
				})

				It("get should hit the preheated seed peers without fetching the file again", Label("preheat", "file", sdk.Name), func() {
					url := testFile.GetDownloadURL()
					Expect(sdk.Preheat(url)).To(Succeed())

					seedClients, err := util.GetPreheatedSeedClients(sdk, url, testFile.GetSha256())
					Expect(err).NotTo(HaveOccurred())

					before, err := util.GetFileServer().RequestCount(testFile)
					Expect(err).NotTo(HaveOccurred())
					Expect(before).To(BeNumerically(">=", 1))

					resp, err := sdk.Get(url, testFile.GetOutputPath(), nil, nil)
					Expect(err).NotTo(HaveOccurred())
					Expect(util.CheckCacheHit(resp, testFile.GetTaskID(), seedClients)).To(BeTrue())

					sha256sum, err := util.CalculateSha256ByOutput([]*util.PodExec{util.RunnerExec()}, testFile.GetOutputPath())
					Expect(err).NotTo(HaveOccurred())
					Expect(testFile.GetSha256()).To(Equal(sha256sum))

					after, err := util.GetFileServer().RequestCount(testFile)
					Expect(err).NotTo(HaveOccurred())
					Expect(after).To(Equal(before))
				})
			})
		}
	}
})

var _ = Describe("Preheat Image", func() {
	image := util.BusyboxImage
	for _, sdk := range util.SDKs {
		Context(fmt.Sprintf("%s image using %s sdk", image.GetReference(), sdk.Name), func() {
			It("get should hit the preheated seed peers for the manifest and every blob", Label("preheat", "image", sdk.Name), func() {
				Expect(sdk.PreheatImage(image.GetReference())).To(Succeed())

				token, err := util.RegistryToken(image.GetRegistry(), image.GetRepository())
				Expect(err).NotTo(HaveOccurred())
				header := http.Header{"Authorization": []string{token}}

				for _, blob := range image.GetBlobs() {
					seedClients, err := util.GetPreheatedSeedClients(sdk, blob.GetURL(), blob.GetSha256())
					Expect(err).NotTo(HaveOccurred())

					resp, err := sdk.Get(blob.GetURL(), blob.GetOutputPath(), nil, header)
					Expect(err).NotTo(HaveOccurred())
					Expect(util.CheckCacheHit(resp, blob.GetTaskID(), seedClients)).To(BeTrue())

					sha256sum, err := util.CalculateSha256ByOutput([]*util.PodExec{util.RunnerExec()}, blob.GetOutputPath())
					Expect(err).NotTo(HaveOccurred())
					Expect(blob.GetSha256()).To(Equal(sha256sum))
				}
			})
		})
	}
})
