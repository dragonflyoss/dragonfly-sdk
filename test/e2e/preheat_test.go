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
	"slices"

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

var _ = Describe("Preheat", func() {
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
					Expect(sdk.Preheat(url, "")).To(Succeed())

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

var _ = Describe("Preheat All Seed Peers", func() {
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

			It("get should hit the seed peers preheated with the all seed peers scope", Label("preheat", "file", "all-seed-peers", sdk.Name), func() {
				url := testFile.GetDownloadURL()
				Expect(sdk.Preheat(url, util.ScopeAllSeedPeers)).To(Succeed())

				seedClients, err := util.GetAllPreheatedSeedClients(url, testFile.GetSha256())
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
})

var _ = Describe("Preheat Image", func() {
	for _, sdk := range util.SDKs {
		Context(fmt.Sprintf("ghcr.io/dragonflyoss/busybox:1.35.0 image using %s sdk", sdk.Name), func() {
			It("get should hit the preheated seed peers for the manifest and every blob", Label("preheat", "image", sdk.Name), func() {
				Expect(sdk.PreheatImage("ghcr.io/dragonflyoss/busybox:1.35.0", "", "")).To(Succeed())

				token, err := util.RegistryToken("ghcr.io", "dragonflyoss/busybox")
				Expect(err).NotTo(HaveOccurred())
				header := http.Header{"Authorization": []string{token}}

				blobs := []util.Blob{
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/busybox/manifests/sha256:e39d9c8ac4963d0b00a5af08678757b44c35ea8eb6be0cdfbeb1282e7f7e6003",
						Sha256: "e39d9c8ac4963d0b00a5af08678757b44c35ea8eb6be0cdfbeb1282e7f7e6003",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/busybox/blobs/sha256:a711f05d33845e2e9deffcfcc5adf082d7c6e97e3e3a881d193d9aae38f092a8",
						Sha256: "a711f05d33845e2e9deffcfcc5adf082d7c6e97e3e3a881d193d9aae38f092a8",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/busybox/blobs/sha256:f643e116a03d9604c344edb345d7592c48cc00f2a4848aaf773411f4fb30d2f5",
						Sha256: "f643e116a03d9604c344edb345d7592c48cc00f2a4848aaf773411f4fb30d2f5",
					},
				}

				for _, blob := range blobs {
					seedClients, err := util.GetPreheatedSeedClients(sdk, blob.URL, blob.Sha256)
					Expect(err).NotTo(HaveOccurred())

					resp, err := sdk.Get(blob.URL, blob.GetOutputPath(), nil, header)
					Expect(err).NotTo(HaveOccurred())
					Expect(util.CheckCacheHit(resp, blob.GetTaskID(), seedClients)).To(BeTrue())

					sha256sum, err := util.CalculateSha256ByOutput([]*util.PodExec{util.RunnerExec()}, blob.GetOutputPath())
					Expect(err).NotTo(HaveOccurred())
					Expect(blob.Sha256).To(Equal(sha256sum))
				}
			})
		})
	}
})

var _ = Describe("Preheat Image All Seed Peers", func() {
	for _, sdk := range util.SDKs {
		Context(fmt.Sprintf("ghcr.io/dragonflyoss/busybox:1.35.0 image using %s sdk", sdk.Name), func() {
			AfterEach(func() {
				_, err := sdk.DeleteImage("ghcr.io/dragonflyoss/busybox:1.35.0", "")
				Expect(err).NotTo(HaveOccurred())
			})

			It("get should hit the seed peers preheated with the all seed peers scope for the manifest and every blob", Label("preheat", "image", "all-seed-peers", sdk.Name), func() {
				Expect(sdk.PreheatImage("ghcr.io/dragonflyoss/busybox:1.35.0", util.ScopeAllSeedPeers, "")).To(Succeed())

				token, err := util.RegistryToken("ghcr.io", "dragonflyoss/busybox")
				Expect(err).NotTo(HaveOccurred())
				header := http.Header{"Authorization": []string{token}}

				blobs := []util.Blob{
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/busybox/manifests/sha256:e39d9c8ac4963d0b00a5af08678757b44c35ea8eb6be0cdfbeb1282e7f7e6003",
						Sha256: "e39d9c8ac4963d0b00a5af08678757b44c35ea8eb6be0cdfbeb1282e7f7e6003",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/busybox/blobs/sha256:a711f05d33845e2e9deffcfcc5adf082d7c6e97e3e3a881d193d9aae38f092a8",
						Sha256: "a711f05d33845e2e9deffcfcc5adf082d7c6e97e3e3a881d193d9aae38f092a8",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/busybox/blobs/sha256:f643e116a03d9604c344edb345d7592c48cc00f2a4848aaf773411f4fb30d2f5",
						Sha256: "f643e116a03d9604c344edb345d7592c48cc00f2a4848aaf773411f4fb30d2f5",
					},
				}

				for _, blob := range blobs {
					seedClients, err := util.GetAllPreheatedSeedClients(blob.URL, blob.Sha256)
					Expect(err).NotTo(HaveOccurred())

					resp, err := sdk.Get(blob.URL, blob.GetOutputPath(), nil, header)
					Expect(err).NotTo(HaveOccurred())
					Expect(util.CheckCacheHit(resp, blob.GetTaskID(), seedClients)).To(BeTrue())

					sha256sum, err := util.CalculateSha256ByOutput([]*util.PodExec{util.RunnerExec()}, blob.GetOutputPath())
					Expect(err).NotTo(HaveOccurred())
					Expect(blob.Sha256).To(Equal(sha256sum))
				}
			})
		})
	}
})

var _ = Describe("Preheat Image Multi-Platform", func() {
	for _, sdk := range util.SDKs {
		Context(fmt.Sprintf("ghcr.io/dragonflyoss/client:v1.5.12 image using %s sdk", sdk.Name), func() {
			AfterEach(func() {
				_, err := sdk.DeleteImage("ghcr.io/dragonflyoss/client:v1.5.12", "")
				Expect(err).NotTo(HaveOccurred())

				_, err = sdk.DeleteImage("ghcr.io/dragonflyoss/client@sha256:cf54d0f3bda39332435c109aed0689a3a0d31f0a00e570cf09aae10dfdf5da9b", "")
				Expect(err).NotTo(HaveOccurred())
			})

			It("preheat should land the manifest and every blob of all the platforms on the preheated seed peers", Label("preheat", "image", "multi-platform", sdk.Name), func() {
				Expect(sdk.PreheatImage("ghcr.io/dragonflyoss/client:v1.5.12", "", "")).To(Succeed())

				amd64Blobs := []util.Blob{
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/manifests/sha256:cf54d0f3bda39332435c109aed0689a3a0d31f0a00e570cf09aae10dfdf5da9b",
						Sha256: "cf54d0f3bda39332435c109aed0689a3a0d31f0a00e570cf09aae10dfdf5da9b",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:9eb2e5ef987d268ebe2d9f1617fd3398f5a170aec8546574ba72d7e2362f89c1",
						Sha256: "9eb2e5ef987d268ebe2d9f1617fd3398f5a170aec8546574ba72d7e2362f89c1",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:dd051e1b8c92ff101fa3071ab97ea4924885942f15644e01c3dc095764e863e3",
						Sha256: "dd051e1b8c92ff101fa3071ab97ea4924885942f15644e01c3dc095764e863e3",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:524382ca7728c7aedabc0faecbe7dadb6f4f0a1dc1bbdbe9d14e00307cae36fa",
						Sha256: "524382ca7728c7aedabc0faecbe7dadb6f4f0a1dc1bbdbe9d14e00307cae36fa",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:64bbbf0249fd6001f88d224e6c752712d4d4e156e2ec3a388791051d8070675c",
						Sha256: "64bbbf0249fd6001f88d224e6c752712d4d4e156e2ec3a388791051d8070675c",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:a4d359a97e3b1f92542a0fa7fdc5e6c1566bb208276d8879460ba3b1a2783f63",
						Sha256: "a4d359a97e3b1f92542a0fa7fdc5e6c1566bb208276d8879460ba3b1a2783f63",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:f7d8f0305de41ab5f3a54fe2c23c8da74e16fccbdec15933da32d4343715e65f",
						Sha256: "f7d8f0305de41ab5f3a54fe2c23c8da74e16fccbdec15933da32d4343715e65f",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:b866e5602329fd674dd5e831b94910ad6f881b232fd2d273da8538668c6b86f9",
						Sha256: "b866e5602329fd674dd5e831b94910ad6f881b232fd2d273da8538668c6b86f9",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:4b4cbff4fd9b9d8b0beb4d61b91e6497b3d9bf16742e23ed1d46b164074a6740",
						Sha256: "4b4cbff4fd9b9d8b0beb4d61b91e6497b3d9bf16742e23ed1d46b164074a6740",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:f1b3ecfe00babdadef5ab7b857343e016a684a33f714defdc3e41a99274367a9",
						Sha256: "f1b3ecfe00babdadef5ab7b857343e016a684a33f714defdc3e41a99274367a9",
					},
				}

				arm64Blobs := []util.Blob{
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/manifests/sha256:f1ad22aa03273e3891a67fd5742e077dd9e9ce2a0ab53e3798dcd55cd2fcf443",
						Sha256: "f1ad22aa03273e3891a67fd5742e077dd9e9ce2a0ab53e3798dcd55cd2fcf443",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:901f53de4911302661c0199f318d976e6f323d9a4a8927d37266c0e18db1df60",
						Sha256: "901f53de4911302661c0199f318d976e6f323d9a4a8927d37266c0e18db1df60",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:2d66c782e4847b8495b17a4b86be1159aa13d140fe4fd17cb1d536953d5114c6",
						Sha256: "2d66c782e4847b8495b17a4b86be1159aa13d140fe4fd17cb1d536953d5114c6",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:5d33eb8f4cd8a1e776847144f5b26534d2646d9a08311cebf87448d4660b1443",
						Sha256: "5d33eb8f4cd8a1e776847144f5b26534d2646d9a08311cebf87448d4660b1443",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:419a5850574de25219e005fd1c9823c50c435fb6899614b1b244c409a1705723",
						Sha256: "419a5850574de25219e005fd1c9823c50c435fb6899614b1b244c409a1705723",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:2ba424c08a17c5fc643e899705586f4fa1e9d4b901d84af4458e2446ae1e5fad",
						Sha256: "2ba424c08a17c5fc643e899705586f4fa1e9d4b901d84af4458e2446ae1e5fad",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:2a2275091837f8f76481fdcb84ca8524a854de3470758e5c4e1f0e57b32e2cba",
						Sha256: "2a2275091837f8f76481fdcb84ca8524a854de3470758e5c4e1f0e57b32e2cba",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:5f704c526ba80e95c21c3a57dda2eae607db44f1d619c068488219da1ef08be0",
						Sha256: "5f704c526ba80e95c21c3a57dda2eae607db44f1d619c068488219da1ef08be0",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:84ba1612e4f058b35f1cce29d6b08dfbda1df72ee870db3466c6c19374fc2bfb",
						Sha256: "84ba1612e4f058b35f1cce29d6b08dfbda1df72ee870db3466c6c19374fc2bfb",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:846332a555385acda2e7d0afdca09da745a9ab0aa202eade90bc46c246b5acbc",
						Sha256: "846332a555385acda2e7d0afdca09da745a9ab0aa202eade90bc46c246b5acbc",
					},
				}

				attestationBlobs := []util.Blob{
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/manifests/sha256:e8ba1e6acad4966302f89ce15bb6a2f84a67bef394485b8a5543d3bf731ed3ea",
						Sha256: "e8ba1e6acad4966302f89ce15bb6a2f84a67bef394485b8a5543d3bf731ed3ea",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
						Sha256: "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:c47b50e1f145d4983ea01bff4daa932c963f1ab5240645255585ceaa709013df",
						Sha256: "c47b50e1f145d4983ea01bff4daa932c963f1ab5240645255585ceaa709013df",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/manifests/sha256:57bbdce6508a6aa52df73985ad567ff1fd650119159c15c64a76a8026048ffaa",
						Sha256: "57bbdce6508a6aa52df73985ad567ff1fd650119159c15c64a76a8026048ffaa",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:32704f4d4e33e495935c4b440886feda3911bc8c4d7a6b644c37f89d9ffe58e5",
						Sha256: "32704f4d4e33e495935c4b440886feda3911bc8c4d7a6b644c37f89d9ffe58e5",
					},
				}

				for _, blob := range slices.Concat(amd64Blobs, arm64Blobs, attestationBlobs) {
					_, err := util.GetPreheatedSeedClients(sdk, blob.URL, blob.Sha256)
					Expect(err).NotTo(HaveOccurred())
				}
			})

			It("preheat with the platform should only land the manifest and every blob of the platform on the preheated seed peers", Label("preheat", "image", "multi-platform", sdk.Name), func() {
				Expect(sdk.PreheatImage("ghcr.io/dragonflyoss/client:v1.5.12", "", "linux/arm64")).To(Succeed())

				amd64Blobs := []util.Blob{
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/manifests/sha256:cf54d0f3bda39332435c109aed0689a3a0d31f0a00e570cf09aae10dfdf5da9b",
						Sha256: "cf54d0f3bda39332435c109aed0689a3a0d31f0a00e570cf09aae10dfdf5da9b",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:9eb2e5ef987d268ebe2d9f1617fd3398f5a170aec8546574ba72d7e2362f89c1",
						Sha256: "9eb2e5ef987d268ebe2d9f1617fd3398f5a170aec8546574ba72d7e2362f89c1",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:dd051e1b8c92ff101fa3071ab97ea4924885942f15644e01c3dc095764e863e3",
						Sha256: "dd051e1b8c92ff101fa3071ab97ea4924885942f15644e01c3dc095764e863e3",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:524382ca7728c7aedabc0faecbe7dadb6f4f0a1dc1bbdbe9d14e00307cae36fa",
						Sha256: "524382ca7728c7aedabc0faecbe7dadb6f4f0a1dc1bbdbe9d14e00307cae36fa",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:64bbbf0249fd6001f88d224e6c752712d4d4e156e2ec3a388791051d8070675c",
						Sha256: "64bbbf0249fd6001f88d224e6c752712d4d4e156e2ec3a388791051d8070675c",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:a4d359a97e3b1f92542a0fa7fdc5e6c1566bb208276d8879460ba3b1a2783f63",
						Sha256: "a4d359a97e3b1f92542a0fa7fdc5e6c1566bb208276d8879460ba3b1a2783f63",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:f7d8f0305de41ab5f3a54fe2c23c8da74e16fccbdec15933da32d4343715e65f",
						Sha256: "f7d8f0305de41ab5f3a54fe2c23c8da74e16fccbdec15933da32d4343715e65f",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:b866e5602329fd674dd5e831b94910ad6f881b232fd2d273da8538668c6b86f9",
						Sha256: "b866e5602329fd674dd5e831b94910ad6f881b232fd2d273da8538668c6b86f9",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:4b4cbff4fd9b9d8b0beb4d61b91e6497b3d9bf16742e23ed1d46b164074a6740",
						Sha256: "4b4cbff4fd9b9d8b0beb4d61b91e6497b3d9bf16742e23ed1d46b164074a6740",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:f1b3ecfe00babdadef5ab7b857343e016a684a33f714defdc3e41a99274367a9",
						Sha256: "f1b3ecfe00babdadef5ab7b857343e016a684a33f714defdc3e41a99274367a9",
					},
				}

				arm64Blobs := []util.Blob{
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/manifests/sha256:f1ad22aa03273e3891a67fd5742e077dd9e9ce2a0ab53e3798dcd55cd2fcf443",
						Sha256: "f1ad22aa03273e3891a67fd5742e077dd9e9ce2a0ab53e3798dcd55cd2fcf443",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:901f53de4911302661c0199f318d976e6f323d9a4a8927d37266c0e18db1df60",
						Sha256: "901f53de4911302661c0199f318d976e6f323d9a4a8927d37266c0e18db1df60",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:2d66c782e4847b8495b17a4b86be1159aa13d140fe4fd17cb1d536953d5114c6",
						Sha256: "2d66c782e4847b8495b17a4b86be1159aa13d140fe4fd17cb1d536953d5114c6",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:5d33eb8f4cd8a1e776847144f5b26534d2646d9a08311cebf87448d4660b1443",
						Sha256: "5d33eb8f4cd8a1e776847144f5b26534d2646d9a08311cebf87448d4660b1443",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:419a5850574de25219e005fd1c9823c50c435fb6899614b1b244c409a1705723",
						Sha256: "419a5850574de25219e005fd1c9823c50c435fb6899614b1b244c409a1705723",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:2ba424c08a17c5fc643e899705586f4fa1e9d4b901d84af4458e2446ae1e5fad",
						Sha256: "2ba424c08a17c5fc643e899705586f4fa1e9d4b901d84af4458e2446ae1e5fad",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:2a2275091837f8f76481fdcb84ca8524a854de3470758e5c4e1f0e57b32e2cba",
						Sha256: "2a2275091837f8f76481fdcb84ca8524a854de3470758e5c4e1f0e57b32e2cba",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:5f704c526ba80e95c21c3a57dda2eae607db44f1d619c068488219da1ef08be0",
						Sha256: "5f704c526ba80e95c21c3a57dda2eae607db44f1d619c068488219da1ef08be0",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:84ba1612e4f058b35f1cce29d6b08dfbda1df72ee870db3466c6c19374fc2bfb",
						Sha256: "84ba1612e4f058b35f1cce29d6b08dfbda1df72ee870db3466c6c19374fc2bfb",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:846332a555385acda2e7d0afdca09da745a9ab0aa202eade90bc46c246b5acbc",
						Sha256: "846332a555385acda2e7d0afdca09da745a9ab0aa202eade90bc46c246b5acbc",
					},
				}

				attestationBlobs := []util.Blob{
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/manifests/sha256:e8ba1e6acad4966302f89ce15bb6a2f84a67bef394485b8a5543d3bf731ed3ea",
						Sha256: "e8ba1e6acad4966302f89ce15bb6a2f84a67bef394485b8a5543d3bf731ed3ea",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
						Sha256: "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:c47b50e1f145d4983ea01bff4daa932c963f1ab5240645255585ceaa709013df",
						Sha256: "c47b50e1f145d4983ea01bff4daa932c963f1ab5240645255585ceaa709013df",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/manifests/sha256:57bbdce6508a6aa52df73985ad567ff1fd650119159c15c64a76a8026048ffaa",
						Sha256: "57bbdce6508a6aa52df73985ad567ff1fd650119159c15c64a76a8026048ffaa",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:32704f4d4e33e495935c4b440886feda3911bc8c4d7a6b644c37f89d9ffe58e5",
						Sha256: "32704f4d4e33e495935c4b440886feda3911bc8c4d7a6b644c37f89d9ffe58e5",
					},
				}

				for _, blob := range arm64Blobs {
					_, err := util.GetPreheatedSeedClients(sdk, blob.URL, blob.Sha256)
					Expect(err).NotTo(HaveOccurred())
				}

				seedClients, err := util.SeedClients()
				Expect(err).NotTo(HaveOccurred())
				Expect(seedClients).To(HaveLen(util.SeedClientReplicas))

				for _, blob := range slices.Concat(amd64Blobs, attestationBlobs) {
					Expect(util.CheckFilesExist(util.PodExecs(seedClients), blob.GetTaskID())).To(BeFalse())
				}
			})

			It("preheat by the digest should only land the manifest and every blob of the platform on the preheated seed peers", Label("preheat", "image", "multi-platform", sdk.Name), func() {
				Expect(sdk.PreheatImage("ghcr.io/dragonflyoss/client@sha256:cf54d0f3bda39332435c109aed0689a3a0d31f0a00e570cf09aae10dfdf5da9b", "", "")).To(Succeed())

				amd64Blobs := []util.Blob{
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/manifests/sha256:cf54d0f3bda39332435c109aed0689a3a0d31f0a00e570cf09aae10dfdf5da9b",
						Sha256: "cf54d0f3bda39332435c109aed0689a3a0d31f0a00e570cf09aae10dfdf5da9b",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:9eb2e5ef987d268ebe2d9f1617fd3398f5a170aec8546574ba72d7e2362f89c1",
						Sha256: "9eb2e5ef987d268ebe2d9f1617fd3398f5a170aec8546574ba72d7e2362f89c1",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:dd051e1b8c92ff101fa3071ab97ea4924885942f15644e01c3dc095764e863e3",
						Sha256: "dd051e1b8c92ff101fa3071ab97ea4924885942f15644e01c3dc095764e863e3",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:524382ca7728c7aedabc0faecbe7dadb6f4f0a1dc1bbdbe9d14e00307cae36fa",
						Sha256: "524382ca7728c7aedabc0faecbe7dadb6f4f0a1dc1bbdbe9d14e00307cae36fa",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:64bbbf0249fd6001f88d224e6c752712d4d4e156e2ec3a388791051d8070675c",
						Sha256: "64bbbf0249fd6001f88d224e6c752712d4d4e156e2ec3a388791051d8070675c",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:a4d359a97e3b1f92542a0fa7fdc5e6c1566bb208276d8879460ba3b1a2783f63",
						Sha256: "a4d359a97e3b1f92542a0fa7fdc5e6c1566bb208276d8879460ba3b1a2783f63",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:f7d8f0305de41ab5f3a54fe2c23c8da74e16fccbdec15933da32d4343715e65f",
						Sha256: "f7d8f0305de41ab5f3a54fe2c23c8da74e16fccbdec15933da32d4343715e65f",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:b866e5602329fd674dd5e831b94910ad6f881b232fd2d273da8538668c6b86f9",
						Sha256: "b866e5602329fd674dd5e831b94910ad6f881b232fd2d273da8538668c6b86f9",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:4b4cbff4fd9b9d8b0beb4d61b91e6497b3d9bf16742e23ed1d46b164074a6740",
						Sha256: "4b4cbff4fd9b9d8b0beb4d61b91e6497b3d9bf16742e23ed1d46b164074a6740",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:f1b3ecfe00babdadef5ab7b857343e016a684a33f714defdc3e41a99274367a9",
						Sha256: "f1b3ecfe00babdadef5ab7b857343e016a684a33f714defdc3e41a99274367a9",
					},
				}

				arm64Blobs := []util.Blob{
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/manifests/sha256:f1ad22aa03273e3891a67fd5742e077dd9e9ce2a0ab53e3798dcd55cd2fcf443",
						Sha256: "f1ad22aa03273e3891a67fd5742e077dd9e9ce2a0ab53e3798dcd55cd2fcf443",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:901f53de4911302661c0199f318d976e6f323d9a4a8927d37266c0e18db1df60",
						Sha256: "901f53de4911302661c0199f318d976e6f323d9a4a8927d37266c0e18db1df60",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:2d66c782e4847b8495b17a4b86be1159aa13d140fe4fd17cb1d536953d5114c6",
						Sha256: "2d66c782e4847b8495b17a4b86be1159aa13d140fe4fd17cb1d536953d5114c6",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:5d33eb8f4cd8a1e776847144f5b26534d2646d9a08311cebf87448d4660b1443",
						Sha256: "5d33eb8f4cd8a1e776847144f5b26534d2646d9a08311cebf87448d4660b1443",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:419a5850574de25219e005fd1c9823c50c435fb6899614b1b244c409a1705723",
						Sha256: "419a5850574de25219e005fd1c9823c50c435fb6899614b1b244c409a1705723",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:2ba424c08a17c5fc643e899705586f4fa1e9d4b901d84af4458e2446ae1e5fad",
						Sha256: "2ba424c08a17c5fc643e899705586f4fa1e9d4b901d84af4458e2446ae1e5fad",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:2a2275091837f8f76481fdcb84ca8524a854de3470758e5c4e1f0e57b32e2cba",
						Sha256: "2a2275091837f8f76481fdcb84ca8524a854de3470758e5c4e1f0e57b32e2cba",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:5f704c526ba80e95c21c3a57dda2eae607db44f1d619c068488219da1ef08be0",
						Sha256: "5f704c526ba80e95c21c3a57dda2eae607db44f1d619c068488219da1ef08be0",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:84ba1612e4f058b35f1cce29d6b08dfbda1df72ee870db3466c6c19374fc2bfb",
						Sha256: "84ba1612e4f058b35f1cce29d6b08dfbda1df72ee870db3466c6c19374fc2bfb",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:846332a555385acda2e7d0afdca09da745a9ab0aa202eade90bc46c246b5acbc",
						Sha256: "846332a555385acda2e7d0afdca09da745a9ab0aa202eade90bc46c246b5acbc",
					},
				}

				attestationBlobs := []util.Blob{
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/manifests/sha256:e8ba1e6acad4966302f89ce15bb6a2f84a67bef394485b8a5543d3bf731ed3ea",
						Sha256: "e8ba1e6acad4966302f89ce15bb6a2f84a67bef394485b8a5543d3bf731ed3ea",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
						Sha256: "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:c47b50e1f145d4983ea01bff4daa932c963f1ab5240645255585ceaa709013df",
						Sha256: "c47b50e1f145d4983ea01bff4daa932c963f1ab5240645255585ceaa709013df",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/manifests/sha256:57bbdce6508a6aa52df73985ad567ff1fd650119159c15c64a76a8026048ffaa",
						Sha256: "57bbdce6508a6aa52df73985ad567ff1fd650119159c15c64a76a8026048ffaa",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:32704f4d4e33e495935c4b440886feda3911bc8c4d7a6b644c37f89d9ffe58e5",
						Sha256: "32704f4d4e33e495935c4b440886feda3911bc8c4d7a6b644c37f89d9ffe58e5",
					},
				}

				for _, blob := range amd64Blobs {
					_, err := util.GetPreheatedSeedClients(sdk, blob.URL, blob.Sha256)
					Expect(err).NotTo(HaveOccurred())
				}

				seedClients, err := util.SeedClients()
				Expect(err).NotTo(HaveOccurred())
				Expect(seedClients).To(HaveLen(util.SeedClientReplicas))

				for _, blob := range slices.Concat(arm64Blobs, attestationBlobs) {
					Expect(util.CheckFilesExist(util.PodExecs(seedClients), blob.GetTaskID())).To(BeFalse())
				}
			})

			It("get should hit the preheated seed peers for the manifest and every blob of the platform", Label("preheat", "image", "multi-platform", sdk.Name), func() {
				Expect(sdk.PreheatImage("ghcr.io/dragonflyoss/client:v1.5.12", "", "unknown/unknown")).To(Succeed())

				token, err := util.RegistryToken("ghcr.io", "dragonflyoss/client")
				Expect(err).NotTo(HaveOccurred())
				header := http.Header{"Authorization": []string{token}}

				attestationBlobs := []util.Blob{
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/manifests/sha256:e8ba1e6acad4966302f89ce15bb6a2f84a67bef394485b8a5543d3bf731ed3ea",
						Sha256: "e8ba1e6acad4966302f89ce15bb6a2f84a67bef394485b8a5543d3bf731ed3ea",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
						Sha256: "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:c47b50e1f145d4983ea01bff4daa932c963f1ab5240645255585ceaa709013df",
						Sha256: "c47b50e1f145d4983ea01bff4daa932c963f1ab5240645255585ceaa709013df",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/manifests/sha256:57bbdce6508a6aa52df73985ad567ff1fd650119159c15c64a76a8026048ffaa",
						Sha256: "57bbdce6508a6aa52df73985ad567ff1fd650119159c15c64a76a8026048ffaa",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:32704f4d4e33e495935c4b440886feda3911bc8c4d7a6b644c37f89d9ffe58e5",
						Sha256: "32704f4d4e33e495935c4b440886feda3911bc8c4d7a6b644c37f89d9ffe58e5",
					},
				}

				for _, blob := range attestationBlobs {
					seedClients, err := util.GetPreheatedSeedClients(sdk, blob.URL, blob.Sha256)
					Expect(err).NotTo(HaveOccurred())

					resp, err := sdk.Get(blob.URL, blob.GetOutputPath(), nil, header)
					Expect(err).NotTo(HaveOccurred())
					Expect(util.CheckCacheHit(resp, blob.GetTaskID(), seedClients)).To(BeTrue())

					sha256sum, err := util.CalculateSha256ByOutput([]*util.PodExec{util.RunnerExec()}, blob.GetOutputPath())
					Expect(err).NotTo(HaveOccurred())
					Expect(blob.Sha256).To(Equal(sha256sum))
				}
			})
		})
	}
})
