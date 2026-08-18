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
	"time"

	commonv2 "d7y.io/api/v2/pkg/apis/common/v2"
	"github.com/stretchr/testify/assert"

	"d7y.io/dragonfly-sdk/client-request/go/internal/hashring"
)

func createTestHost(id, ip string, port int32, hostType uint32) *commonv2.Host {
	return &commonv2.Host{
		Id:       id,
		Ip:       ip,
		Port:     port,
		Type:     hostType,
		Hostname: "host-" + id,
		Name:     "host-" + id,
	}
}

func createTestSelector() *SeedPeerSelector {
	return &SeedPeerSelector{
		healthCheckInterval: 10 * time.Second,
		requestTimeout:      5 * time.Second,
		hosts:               make(map[string]*commonv2.Host),
		ring:                hashring.New(1),
		done:                make(chan struct{}),
	}
}

func addTestHost(s *SeedPeerSelector, host *commonv2.Host) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.hosts[host.Name] = host
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
