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

package request

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetIntoWithSeedPeers(t *testing.T) {
	assert := assert.New(t)

	proxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello dragonfly")
	})

	proxy, err := NewWithSeedPeers([]SeedPeer{
		{Host: createSeedPeerHost("seed-peer-1", 0, proxyPort), Healthy: true},
	})
	assert.NoError(err)

	var buf bytes.Buffer
	resp, err := proxy.GetInto(context.Background(), NewGetRequest("http://example.com/file.txt", WithGetRequestReplicas(1)), &buf)
	assert.NoError(err)

	assert.True(resp.Success)
	assert.Equal("hello dragonfly", buf.String())
}

func TestNewWithSeedPeersInvalid(t *testing.T) {
	tests := []struct {
		name      string
		seedPeers []SeedPeer
		opts      []ProxyWithSeedPeersOption
	}{
		{
			name:      "nil seed peers",
			seedPeers: nil,
		},
		{
			name:      "empty seed peers",
			seedPeers: []SeedPeer{},
		},
		{
			name:      "seed peer without a host",
			seedPeers: []SeedPeer{{Healthy: true}},
		},
		{
			name:      "seed peer without a name",
			seedPeers: []SeedPeer{{Host: createSeedPeerHost("", 0, 4001), Healthy: true}},
		},
		{
			name: "duplicated seed peer names",
			seedPeers: []SeedPeer{
				{Host: createSeedPeerHost("seed-peer-1", 0, 4001), Healthy: true},
				{Host: createSeedPeerHost("seed-peer-1", 0, 4002), Healthy: true},
			},
		},
		{
			name:      "too many retries",
			seedPeers: []SeedPeer{{Host: createSeedPeerHost("seed-peer-1", 0, 4001), Healthy: true}},
			opts:      []ProxyWithSeedPeersOption{WithProxyWithSeedPeersMaxRetries(11)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)
			_, err := NewWithSeedPeers(tc.seedPeers, tc.opts...)
			assert.ErrorIs(err, ErrInvalidArgument)
		})
	}
}

func TestUpdateSeedPeersKeepsTheSeedPeersWhenGivenInvalidOnes(t *testing.T) {
	tests := []struct {
		name      string
		seedPeers []SeedPeer
	}{
		{
			name:      "nil seed peers",
			seedPeers: nil,
		},
		{
			name:      "empty seed peers",
			seedPeers: []SeedPeer{},
		},
		{
			name:      "seed peers without a host",
			seedPeers: []SeedPeer{{Healthy: true}},
		},
		{
			name:      "seed peer without a name",
			seedPeers: []SeedPeer{{Host: createSeedPeerHost("", 0, 4002), Healthy: true}},
		},
		{
			name: "duplicated seed peer names",
			seedPeers: []SeedPeer{
				{Host: createSeedPeerHost("seed-peer-2", 0, 4002), Healthy: true},
				{Host: createSeedPeerHost("seed-peer-2", 0, 4003), Healthy: true},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)
			host := createSeedPeerHost("seed-peer-1", 0, 4001)
			proxy, err := NewWithSeedPeers([]SeedPeer{{Host: host, Healthy: true}})
			assert.NoError(err)

			proxy.UpdateSeedPeers(tc.seedPeers)

			endpoints, err := proxy.LookupEndpoints(context.Background(), NewGetRequest("http://example.com/file.txt", WithGetRequestReplicas(1)))
			assert.NoError(err)
			assert.Equal([]string{proxy.proxy.seedPeerSelector.ProxyEndpoint(host)}, endpoints)
		})
	}
}

func TestUpdateSeedPeersReplacesTheSeedPeers(t *testing.T) {
	assert := assert.New(t)
	proxy, err := NewWithSeedPeers([]SeedPeer{{Host: createSeedPeerHost("seed-peer-1", 0, 4001), Healthy: true}})
	assert.NoError(err)

	host := createSeedPeerHost("seed-peer-2", 0, 4002)
	proxy.UpdateSeedPeers([]SeedPeer{{Host: host, Healthy: true}})

	endpoints, err := proxy.LookupEndpoints(context.Background(), NewGetRequest("http://example.com/file.txt", WithGetRequestReplicas(1)))
	assert.NoError(err)
	assert.Equal([]string{proxy.proxy.seedPeerSelector.ProxyEndpoint(host)}, endpoints)
}
