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

package selector

import (
	"fmt"
	"sync"
	"testing"

	commonv2 "d7y.io/api/v2/pkg/apis/common/v2"
	"d7y.io/hashring"
	"github.com/stretchr/testify/assert"
)

func createTestHost(id, ip string, port int32, hostType uint32) *commonv2.Host {
	return &commonv2.Host{
		Id:        id,
		Ip:        ip,
		Port:      port,
		Type:      hostType,
		ProxyPort: port,
		Hostname:  "host-" + id,
		Name:      "host-" + id,
	}
}

func createTestSelector() *SeedPeerSelector {
	s := New()
	s.ring = hashring.New(hashring.WithVNodesPerNode(1))
	return s
}

func createTestSeedPeers(count int, unhealthyID string) []SeedPeer {
	seedPeers := make([]SeedPeer, 0, count)
	for i := 1; i <= count; i++ {
		host := createTestHost(fmt.Sprint(i), fmt.Sprintf("192.168.1.%d", i), 8080, 1)
		seedPeers = append(seedPeers, SeedPeer{Host: host, Healthy: host.Id != unhealthyID})
	}

	return seedPeers
}

func selectIDs(t *testing.T, s *SeedPeerSelector, taskID string, replicas uint32) []string {
	t.Helper()

	hosts, err := s.Select(taskID, replicas)
	assert.NoError(t, err)

	ids := make([]string, 0, len(hosts))
	for _, host := range hosts {
		ids = append(ids, host.Id)
	}

	return ids
}

func addTestHost(s *SeedPeerSelector, host *commonv2.Host) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.hosts[host.Name] = SeedPeer{Host: host, Healthy: true}
	s.ring.Add(host.Name)
}

func TestSelectWithNoHosts(t *testing.T) {
	assert := assert.New(t)
	s := createTestSelector()

	_, err := s.Select("test-task", 2)
	assert.Error(err)
}

func TestSelectWithSingleHost(t *testing.T) {
	assert := assert.New(t)
	s := createTestSelector()
	addTestHost(s, createTestHost("1", "192.168.1.1", 8080, 1))

	hosts, err := s.Select("test-task", 1)
	assert.NoError(err)

	assert.Len(hosts, 1)
	assert.Equal("1", hosts[0].Id)
	assert.Equal("192.168.1.1", hosts[0].Ip)
}

func TestSelectWithMultipleHosts(t *testing.T) {
	assert := assert.New(t)
	s := createTestSelector()
	for i := 1; i <= 5; i++ {
		addTestHost(s, createTestHost(fmt.Sprint(i), fmt.Sprintf("192.168.1.%d", i), 8080, 1))
	}

	hosts, err := s.Select("test-task", 3)
	assert.NoError(err)
	assert.Len(hosts, 3)

	seen := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		seen[host.Id] = struct{}{}
	}
	assert.Len(seen, 3)
}

func TestSelectReplicasExceedsAvailable(t *testing.T) {
	assert := assert.New(t)
	s := createTestSelector()
	for i := 1; i <= 2; i++ {
		addTestHost(s, createTestHost(fmt.Sprint(i), fmt.Sprintf("192.168.1.%d", i), 8080, 1))
	}

	hosts, err := s.Select("test-task", 5)
	assert.NoError(err)
	assert.Len(hosts, 2)
}

func TestSelectAll(t *testing.T) {
	assert := assert.New(t)
	s := createTestSelector()

	_, err := s.SelectAll()
	assert.Error(err)

	for i := 1; i <= 5; i++ {
		addTestHost(s, createTestHost(fmt.Sprint(i), fmt.Sprintf("192.168.1.%d", i), 8080, 1))
	}

	hosts, err := s.SelectAll()
	assert.NoError(err)
	assert.Len(hosts, 5)

	seen := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		seen[host.Id] = struct{}{}
	}
	assert.Len(seen, 5)
}

func TestSelectAllSkipsUnhealthySeedPeers(t *testing.T) {
	assert := assert.New(t)
	s := New()
	s.Update(createTestSeedPeers(2, "2"))

	hosts, err := s.SelectAll()
	assert.NoError(err)
	assert.Len(hosts, 1)
	assert.Equal("1", hosts[0].Id)

	s.Update(createTestSeedPeers(1, "1"))
	_, err = s.SelectAll()
	assert.Error(err)
}

func TestSelectConsistency(t *testing.T) {
	assert := assert.New(t)
	s := createTestSelector()
	for i := 1; i <= 5; i++ {
		addTestHost(s, createTestHost(fmt.Sprint(i), fmt.Sprintf("192.168.1.%d", i), 8080, 1))
	}

	ids := func(hosts []*commonv2.Host) []string {
		var out []string
		for _, h := range hosts {
			out = append(out, h.Id)
		}
		return out
	}

	result1, err := s.Select("consistent-task", 3)
	assert.NoError(err)
	result2, err := s.Select("consistent-task", 3)
	assert.NoError(err)
	result3, err := s.Select("consistent-task", 3)
	assert.NoError(err)

	assert.Equal(ids(result1), ids(result2))
	assert.Equal(ids(result2), ids(result3))
}

func TestConcurrentSelect(t *testing.T) {
	assert := assert.New(t)
	s := createTestSelector()
	for i := 1; i <= 5; i++ {
		addTestHost(s, createTestHost(fmt.Sprint(i), fmt.Sprintf("192.168.1.%d", i), 8080, 1))
	}

	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			hosts, err := s.Select(fmt.Sprintf("concurrent-task-%d", i), 2)
			assert.NoError(err)
			assert.Len(hosts, 2)
		})
	}
	wg.Wait()
}

func TestConcurrentUpdate(t *testing.T) {
	assert := assert.New(t)
	for range 100 {
		s := New()
		s.Update(createTestSeedPeers(2, ""))

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := range 1000 {
				s.Update(createTestSeedPeers(2, fmt.Sprint(i%2+1)))
			}
		}()
		go func() {
			defer wg.Done()
			s.Update(createTestSeedPeers(3, ""))
		}()
		wg.Wait()

		hostNames := make(map[string]struct{}, len(s.hosts))
		for name := range s.hosts {
			hostNames[name] = struct{}{}
		}

		ringNames := make(map[string]struct{}, len(s.hosts))
		for _, vnode := range s.ring.GetWithReplicas("test-task", s.ring.Len()) {
			ringNames[vnode.Name()] = struct{}{}
		}
		assert.Equal(hostNames, ringNames)
	}
}

func TestUpdateSkipsSeedPeersWithoutAHost(t *testing.T) {
	assert := assert.New(t)
	s := New()
	s.Update(append(createTestSeedPeers(1, ""), SeedPeer{Healthy: true}))

	assert.Len(s.hosts, 1)
}

func TestSelectServesATaskOnlyFromItsHealthyReplicas(t *testing.T) {
	assert := assert.New(t)
	s := New()
	s.Update(createTestSeedPeers(5, ""))

	taskIDs := make([]string, 64)
	healthySelections := make([][]string, len(taskIDs))
	for i := range taskIDs {
		taskIDs[i] = fmt.Sprintf("task-%d", i)
		healthySelections[i] = selectIDs(t, s, taskIDs[i], 2)
	}

	s.Update(createTestSeedPeers(5, "3"))
	for i, taskID := range taskIDs {
		var expected []string
		for _, id := range healthySelections[i] {
			if id != "3" {
				expected = append(expected, id)
			}
		}
		assert.Equal(expected, selectIDs(t, s, taskID, 2), taskID)
	}

	s.Update(createTestSeedPeers(5, ""))
	for i, taskID := range taskIDs {
		assert.Equal(healthySelections[i], selectIDs(t, s, taskID, 2), taskID)
	}
}

func TestSelectFailsWhenEveryReplicaIsUnhealthy(t *testing.T) {
	assert := assert.New(t)
	s := New()
	s.Update([]SeedPeer{
		{Host: createTestHost("1", "192.168.1.1", 8080, 1), Healthy: false},
		{Host: createTestHost("2", "192.168.1.2", 8080, 1), Healthy: false},
		{Host: createTestHost("3", "192.168.1.3", 8080, 1), Healthy: true},
	})

	var taskID string
	for i := range 64 {
		taskID = fmt.Sprintf("task-%d", i)
		if s.ring.GetWithReplicas(taskID, 1)[0].Name() != "host-3" {
			break
		}
	}

	_, err := s.Select(taskID, 1)
	assert.Error(err)
}

func TestOnFailureEjectsAProxyAfterConsecutiveFailuresUntilASuccess(t *testing.T) {
	assert := assert.New(t)
	s := New()
	s.Update(createTestSeedPeers(1, ""))
	replica := selectIDs(t, s, "test-task", 1)
	endpoint := s.ProxyEndpoint(s.hosts["host-"+replica[0]].Host)

	for range MaxFailedCount - 1 {
		s.OnFailure(endpoint)
	}
	assert.False(s.IsEjected(endpoint))

	s.OnFailure(endpoint)
	assert.True(s.IsEjected(endpoint))
	assert.Equal(replica, selectIDs(t, s, "test-task", 1))

	s.OnSuccess(endpoint)
	assert.False(s.IsEjected(endpoint))
}
