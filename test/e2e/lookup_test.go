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

var _ = Describe("Lookup Endpoints", func() {
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

			It("get with each endpoint and with all the endpoints should hit the preheated seed peers", Label("lookup", "file", sdk.Name), func() {
				url := testFile.GetDownloadURL()
				Expect(sdk.Preheat(url)).To(Succeed())

				seedClients, err := util.GetPreheatedSeedClients(sdk, url, testFile.GetSha256())
				Expect(err).NotTo(HaveOccurred())

				before, err := util.GetFileServer().RequestCount(testFile)
				Expect(err).NotTo(HaveOccurred())

				var endpoints []string
				for _, seedClient := range seedClients {
					resp, err := sdk.Get(url, testFile.GetOutputPath(), []string{seedClient.ProxyEndpoint()}, nil)
					Expect(err).NotTo(HaveOccurred())
					Expect(util.CheckCacheHit(resp, testFile.GetTaskID(), []*util.SeedClient{seedClient})).To(BeTrue())

					sha256sum, err := util.CalculateSha256ByOutput([]*util.PodExec{util.RunnerExec()}, testFile.GetOutputPath())
					Expect(err).NotTo(HaveOccurred())
					Expect(testFile.GetSha256()).To(Equal(sha256sum))

					endpoints = append(endpoints, seedClient.ProxyEndpoint())
				}

				resp, err := sdk.Get(url, testFile.GetOutputPath(), endpoints, nil)
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

	Context("1MiB file using every sdk", func() {
		var (
			testFile *util.File
			err      error
		)

		BeforeEach(func() {
			testFile, err = util.GetFileServer().GenerateFile(util.FileSize1MiB)
			Expect(err).NotTo(HaveOccurred())
			Expect(testFile).NotTo(BeNil())
		})

		AfterEach(func() {
			err = util.GetFileServer().DeleteFile(testFile)
			Expect(err).NotTo(HaveOccurred())
		})

		It("every sdk should look up the same endpoints", Label("lookup", "consistency"), func() {
			var expected []string
			for _, sdk := range util.SDKs {
				endpoints, err := sdk.LookupEndpoints(testFile.GetDownloadURL())
				Expect(err).NotTo(HaveOccurred())
				Expect(endpoints).To(HaveLen(util.DefaultReplicas))

				if expected == nil {
					expected = endpoints
					continue
				}

				Expect(endpoints).To(Equal(expected))
			}
		})
	})
})
