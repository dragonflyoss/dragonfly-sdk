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

package util

import (
	"fmt"
	"net/http"
	"slices"
)

const (
	// DefaultReplicas is the default number of seed peers serving a task in the
	// SDKs, the preheats land the task on this many seed peers.
	DefaultReplicas = 2
)

// GetPreheatedSeedClients looks up the seed clients the SDK selects for the url
// and checks the preheat landed the task on exactly them: they hold the task
// content matching the sha256 and the other seed clients do not.
func GetPreheatedSeedClients(sdk *SDK, url, sha256 string) ([]*SeedClient, error) {
	endpoints, err := sdk.LookupEndpoints(url)
	if err != nil {
		return nil, err
	}

	if len(endpoints) != DefaultReplicas {
		return nil, fmt.Errorf("expected %d endpoints, got %v", DefaultReplicas, endpoints)
	}

	seedClients, err := SeedClients()
	if err != nil {
		return nil, err
	}

	if len(seedClients) != SeedClientReplicas {
		return nil, fmt.Errorf("expected %d seed clients, got %d", SeedClientReplicas, len(seedClients))
	}

	taskID := TaskID(url)
	var selected, unselected []*SeedClient
	for _, seedClient := range seedClients {
		if slices.Contains(endpoints, seedClient.ProxyEndpoint()) {
			selected = append(selected, seedClient)
			continue
		}

		unselected = append(unselected, seedClient)
	}

	if len(selected) != DefaultReplicas {
		return nil, fmt.Errorf("expected endpoints %v to be seed clients, got %d", endpoints, len(selected))
	}

	for _, seedClient := range selected {
		sha256sum, err := CalculateSha256ByTaskID([]*PodExec{seedClient.Pod}, taskID)
		if err != nil {
			return nil, fmt.Errorf("seed client %s should hold task %s: %w", seedClient.IP, taskID, err)
		}

		if sha256sum != sha256 {
			return nil, fmt.Errorf("seed client %s holds task %s with sha256 %s, expected %s", seedClient.IP, taskID, sha256sum, sha256)
		}
	}

	if CheckFilesExist(podExecs(unselected), taskID) {
		return nil, fmt.Errorf("task %s should only be held by the seed clients %v", taskID, endpoints)
	}

	return selected, nil
}

// CheckCacheHit reports whether the response of a get was served from the cache
// of one of the seed clients: it succeeded and carries the task id, the ip of
// one of the seed clients and the download finished flag.
func CheckCacheHit(resp *GetResponse, taskID string, seedClients []*SeedClient) bool {
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("expected status %d, got %d\n", http.StatusOK, resp.StatusCode)
		return false
	}

	if resp.Header[HeaderTaskID] != taskID {
		fmt.Printf("expected task id %s, got %q\n", taskID, resp.Header[HeaderTaskID])
		return false
	}

	if resp.Header[HeaderTaskDownloadFinished] != "true" {
		fmt.Printf("expected the task to be finished before the get, got %q\n", resp.Header[HeaderTaskDownloadFinished])
		return false
	}

	serverIP := resp.Header[HeaderServerIP]
	if !slices.ContainsFunc(seedClients, func(seedClient *SeedClient) bool { return seedClient.IP == serverIP }) {
		fmt.Printf("expected the get to be served by one of %v, got %q\n", ips(seedClients), serverIP)
		return false
	}

	return true
}

// podExecs returns the pods of the seed clients.
func podExecs(seedClients []*SeedClient) []*PodExec {
	pods := make([]*PodExec, 0, len(seedClients))
	for _, seedClient := range seedClients {
		pods = append(pods, seedClient.Pod)
	}

	return pods
}

// ips returns the ips of the seed clients.
func ips(seedClients []*SeedClient) []string {
	ips := make([]string, 0, len(seedClients))
	for _, seedClient := range seedClients {
		ips = append(ips, seedClient.IP)
	}

	return ips
}
