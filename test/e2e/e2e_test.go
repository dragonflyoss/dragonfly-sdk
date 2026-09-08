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

// Package e2e runs the client-request SDKs against a Dragonfly cluster in
// kind: preheating files and OCI images to the seed peers, then downloading
// them and checking the downloads hit the preheated seed peers. Every spec
// runs against both the Go and the Rust SDK through their drivers.
package e2e

import (
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint
	. "github.com/onsi/gomega"    //nolint

	"d7y.io/dragonfly-sdk/test/e2e/util"
)

const (
	// seedPeersAnnounceWait is how long to wait for every seed peer to announce
	// to the scheduler after they are ready, since the SDKs select the seed
	// peers by the consistent hash ring over all of them.
	seedPeersAnnounceWait = 1 * time.Minute
)

var _ = BeforeSuite(func() {
	fs, err := util.NewFileServer()
	Expect(err).NotTo(HaveOccurred())
	Expect(fs).NotTo(BeNil())

	time.Sleep(seedPeersAnnounceWait)
})

var _ = AfterSuite(func() {
	for _, server := range util.Servers {
		podNames, err := util.GetPodNames(server.Namespace, server.Name)
		if err != nil {
			fmt.Printf("get pods of %s error: %s\n", server.Name, err)
			continue
		}

		for _, podName := range podNames {
			fmt.Printf("\n------------------------------ Get %s Artifact Started ------------------------------\n", podName)
			if err := util.UploadArtifactStdout(server.Namespace, podName, server.Name); err != nil {
				fmt.Printf("upload pod %s artifact stdout file error: %v\n", podName, err)
			}
			fmt.Printf("------------------------------ Get %s Artifact Finished ------------------------------\n", podName)
		}
	}

	if err := util.GetFileServer().Purge(); err != nil {
		fmt.Printf("failed to purge the e2e file server: %v\n", err)
	}
})

// TestE2E is the root of e2e test function
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "dragonfly sdk e2e test suite")
}
