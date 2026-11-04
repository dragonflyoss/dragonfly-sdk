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
	"time"

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
				Expect(sdk.Preheat(url, "")).To(Succeed())

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

		It("every sdk should look up the same seed clients after one of them restarts", Label("lookup", "restart"), func() {
			url := testFile.GetDownloadURL()
			before := make(map[string][]string, len(util.SDKs))
			for _, sdk := range util.SDKs {
				endpoints, err := sdk.LookupEndpoints(url)
				Expect(err).NotTo(HaveOccurred())
				Expect(endpoints).To(HaveLen(util.DefaultReplicas))

				names, err := util.SeedClientNames(endpoints)
				Expect(err).NotTo(HaveOccurred())
				before[sdk.Name] = names
			}

			// Restart the first seed client selected for the task, the statefulset
			// recreates the pod with the same name and a new ip.
			podName := before[util.SDKs[0].Name][0]
			uid, err := util.PodUID(util.DragonflyNamespace, podName)
			Expect(err).NotTo(HaveOccurred())

			out, err := util.KubeCtlCommand("-n", util.DragonflyNamespace, "delete", "pod", podName).CombinedOutput()
			fmt.Println(string(out))
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() bool {
				newUID, err := util.PodUID(util.DragonflyNamespace, podName)
				return err == nil && newUID != uid && util.IsPodReady(util.DragonflyNamespace, podName)
			}, 5*time.Minute, 5*time.Second).Should(BeTrue())

			// Wait for the seed client to announce itself to the scheduler, then
			// the same seed clients must be selected since the hashring is keyed
			// by their names.
			for _, sdk := range util.SDKs {
				Eventually(func() ([]string, error) {
					endpoints, err := sdk.LookupEndpoints(url)
					if err != nil {
						return nil, err
					}

					return util.SeedClientNames(endpoints)
				}, 3*time.Minute, 5*time.Second).Should(Equal(before[sdk.Name]))
			}
		})
	})
})
