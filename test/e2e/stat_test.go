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
	"slices"

	. "github.com/onsi/ginkgo/v2" //nolint
	. "github.com/onsi/gomega"    //nolint

	"d7y.io/dragonfly-sdk/test/e2e/util"
)

var _ = Describe("Stat Image", func() {
	for _, sdk := range util.SDKs {
		Context(fmt.Sprintf("ghcr.io/dragonflyoss/busybox:1.35.0 image using %s sdk", sdk.Name), func() {
			AfterEach(func() {
				_, err := sdk.DeleteImage("ghcr.io/dragonflyoss/busybox:1.35.0", "")
				Expect(err).NotTo(HaveOccurred())
			})

			It("stat should report the layers cached by the preheated seed peers", Label("stat", "image", sdk.Name), func() {
				Expect(sdk.PreheatImage("ghcr.io/dragonflyoss/busybox:1.35.0", "", "")).To(Succeed())

				blobs := []util.Blob{
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/busybox/blobs/sha256:a711f05d33845e2e9deffcfcc5adf082d7c6e97e3e3a881d193d9aae38f092a8",
						Sha256: "a711f05d33845e2e9deffcfcc5adf082d7c6e97e3e3a881d193d9aae38f092a8",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/busybox/blobs/sha256:f643e116a03d9604c344edb345d7592c48cc00f2a4848aaf773411f4fb30d2f5",
						Sha256: "f643e116a03d9604c344edb345d7592c48cc00f2a4848aaf773411f4fb30d2f5",
					},
				}

				var urls []string
				for _, blob := range blobs {
					urls = append(urls, blob.URL)
				}

				resp, err := sdk.StatImage("ghcr.io/dragonflyoss/busybox:1.35.0", "")
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.Layers).To(ConsistOf(urls))

				for _, blob := range blobs {
					seedClients, err := util.GetPreheatedSeedClients(sdk, blob.URL, blob.Sha256)
					Expect(err).NotTo(HaveOccurred())

					var ips []string
					for _, seedClient := range seedClients {
						ips = append(ips, seedClient.IP)
					}

					var cachedIPs []string
					for _, peer := range resp.Peers {
						for _, layer := range peer.CachedLayers {
							if layer.URL == blob.URL && layer.IsFinished {
								cachedIPs = append(cachedIPs, peer.IP)
							}
						}
					}
					Expect(cachedIPs).To(ConsistOf(ips))
				}
			})

			It("stat should report no seed peer after the image is deleted", Label("stat", "image", sdk.Name), func() {
				Expect(sdk.PreheatImage("ghcr.io/dragonflyoss/busybox:1.35.0", "", "")).To(Succeed())

				_, err := sdk.DeleteImage("ghcr.io/dragonflyoss/busybox:1.35.0", "")
				Expect(err).NotTo(HaveOccurred())

				resp, err := sdk.StatImage("ghcr.io/dragonflyoss/busybox:1.35.0", "")
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.Layers).To(HaveLen(2))
				Expect(resp.Peers).To(BeEmpty())
			})
		})
	}
})

var _ = Describe("Stat Image Multi-Platform", func() {
	for _, sdk := range util.SDKs {
		Context(fmt.Sprintf("ghcr.io/dragonflyoss/client:v1.5.12 image using %s sdk", sdk.Name), func() {
			AfterEach(func() {
				_, err := sdk.DeleteImage("ghcr.io/dragonflyoss/client:v1.5.12", "")
				Expect(err).NotTo(HaveOccurred())
			})

			It("stat with the platform should only report the layers of the platform", Label("stat", "image", "multi-platform", sdk.Name), func() {
				Expect(sdk.PreheatImage("ghcr.io/dragonflyoss/client:v1.5.12", "", "linux/arm64")).To(Succeed())

				amd64Blobs := []util.Blob{
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
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
						Sha256: "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:c47b50e1f145d4983ea01bff4daa932c963f1ab5240645255585ceaa709013df",
						Sha256: "c47b50e1f145d4983ea01bff4daa932c963f1ab5240645255585ceaa709013df",
					},
					{
						URL:    "https://ghcr.io/v2/dragonflyoss/client/blobs/sha256:32704f4d4e33e495935c4b440886feda3911bc8c4d7a6b644c37f89d9ffe58e5",
						Sha256: "32704f4d4e33e495935c4b440886feda3911bc8c4d7a6b644c37f89d9ffe58e5",
					},
				}

				var arm64URLs []string
				for _, blob := range arm64Blobs {
					arm64URLs = append(arm64URLs, blob.URL)
				}

				resp, err := sdk.StatImage("ghcr.io/dragonflyoss/client:v1.5.12", "linux/arm64")
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.Layers).To(ConsistOf(arm64URLs))

				for _, blob := range arm64Blobs {
					seedClients, err := util.GetPreheatedSeedClients(sdk, blob.URL, blob.Sha256)
					Expect(err).NotTo(HaveOccurred())

					var ips []string
					for _, seedClient := range seedClients {
						ips = append(ips, seedClient.IP)
					}

					var cachedIPs []string
					for _, peer := range resp.Peers {
						for _, layer := range peer.CachedLayers {
							if layer.URL == blob.URL && layer.IsFinished {
								cachedIPs = append(cachedIPs, peer.IP)
							}
						}
					}
					Expect(cachedIPs).To(ConsistOf(ips))
				}

				var urls []string
				for _, blob := range slices.Concat(amd64Blobs, arm64Blobs, attestationBlobs) {
					urls = append(urls, blob.URL)
				}

				resp, err = sdk.StatImage("ghcr.io/dragonflyoss/client:v1.5.12", "")
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.Layers).To(ConsistOf(urls))

				for _, peer := range resp.Peers {
					for _, layer := range peer.CachedLayers {
						Expect(arm64URLs).To(ContainElement(layer.URL))
					}
				}
			})
		})
	}
})
