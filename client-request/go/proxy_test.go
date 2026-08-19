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
	"io"
	"net/http"
	"testing"
	"time"

	commonv2 "d7y.io/api/v2/pkg/apis/common/v2"
	"github.com/stretchr/testify/assert"
)

func TestNewSuccess(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	p, err := New(context.Background(), endpoint)
	assert.NoError(err)
	assert.Equal(uint8(1), p.maxRetries)
	p.Close()
}

func TestNewEmptyEndpoint(t *testing.T) {
	assert := assert.New(t)
	_, err := New(context.Background(), "")
	assert.ErrorIs(err, ErrInvalidArgument)
}

func TestNewInvalidRetryTimes(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	_, err := New(context.Background(), endpoint, WithProxyMaxRetries(11))
	assert.ErrorIs(err, ErrInvalidArgument)
}

func TestNewInvalidHealthCheckInterval(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	_, err := New(context.Background(), endpoint, WithProxyHealthCheckInterval(0))
	assert.ErrorIs(err, ErrInvalidArgument)
}

func TestGet(t *testing.T) {
	assert := assert.New(t)

	proxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo-Use-P2P", r.Header.Get("X-Dragonfly-Use-P2P"))
		w.Header().Set("X-Echo-Tag", r.Header.Get("X-Dragonfly-Tag"))
		w.Header().Set("X-Echo-Application", r.Header.Get("X-Dragonfly-Application"))
		fmt.Fprint(w, "hello dragonfly")
	})
	port := setupMockSeedPeer(t, nil)
	endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, proxyPort)})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewGetRequest(
		"http://example.com/file.txt",
		WithGetRequestReplicas(1),
		WithGetRequestTag("tag1"),
		WithGetRequestApplication("app1"),
	)

	resp, err := proxy.Get(context.Background(), req)
	assert.NoError(err)
	defer resp.Body.Close()

	assert.True(resp.Success)
	assert.Equal(http.StatusOK, resp.StatusCode)
	assert.Equal("true", resp.Header.Get("X-Echo-Use-P2P"))
	assert.Equal("tag1", resp.Header.Get("X-Echo-Tag"))
	assert.Equal("app1", resp.Header.Get("X-Echo-Application"))

	body, err := io.ReadAll(resp.Body)
	assert.NoError(err)
	assert.Equal("hello dragonfly", string(body))
}

func TestGetInto(t *testing.T) {
	assert := assert.New(t)

	proxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello dragonfly")
	})
	port := setupMockSeedPeer(t, nil)
	endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, proxyPort)})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	var buf bytes.Buffer
	resp, err := proxy.GetInto(context.Background(), NewGetRequest("http://example.com/file.txt", WithGetRequestReplicas(1)), &buf)
	assert.NoError(err)

	assert.True(resp.Success)
	assert.Equal(http.StatusOK, resp.StatusCode)
	assert.Nil(resp.Body)
	assert.Equal("hello dragonfly", buf.String())
}

func TestGetErrorTypes(t *testing.T) {
	proxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
		if errorType := r.Header.Get("X-Dragonfly-Tag"); errorType != "" {
			w.Header().Set("X-Dragonfly-Error-Type", errorType)
		}

		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	})
	port := setupMockSeedPeer(t, nil)
	endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, proxyPort)})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(t, err)
	defer proxy.Close()

	get := func(tag string) error {
		opts := []GetRequestOption{WithGetRequestReplicas(1)}
		if tag != "" {
			opts = append(opts, WithGetRequestTag(tag))
		}

		_, err := proxy.Get(context.Background(), NewGetRequest("http://example.com/file.txt", opts...))
		return err
	}

	t.Run("backend", func(t *testing.T) {
		var backendErr *BackendError
		err := get("backend")
		assert.ErrorAs(t, err, &backendErr)
		assert.Equal(t, "boom", backendErr.Message)
		assert.Equal(t, http.StatusInternalServerError, backendErr.StatusCode)
	})

	t.Run("proxy", func(t *testing.T) {
		var proxyErr *ProxyError
		err := get("proxy")
		assert.ErrorAs(t, err, &proxyErr)
		assert.Equal(t, "boom", proxyErr.Message)
	})

	t.Run("dfdaemon", func(t *testing.T) {
		var dfdaemonErr *DfdaemonError
		err := get("dfdaemon")
		assert.ErrorAs(t, err, &dfdaemonErr)
		assert.Equal(t, "boom", dfdaemonErr.Message)
	})

	t.Run("unknown status code", func(t *testing.T) {
		var proxyErr *ProxyError
		err := get("")
		assert.ErrorAs(t, err, &proxyErr)
		assert.Contains(t, proxyErr.Message, "unexpected status code")
	})
}

func TestGetScattersAcrossReplicas(t *testing.T) {
	assert := assert.New(t)

	badProxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	goodProxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	})
	endpoint := setupMockScheduler(t, []*commonv2.Host{
		createSeedPeerHost("seed-peer-1", setupMockSeedPeer(t, nil), badProxyPort),
		createSeedPeerHost("seed-peer-2", setupMockSeedPeer(t, nil), goodProxyPort),
	})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	resp, err := proxy.Get(context.Background(), NewGetRequest("http://example.com/file.txt", WithGetRequestReplicas(2)))
	assert.NoError(err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	assert.NoError(err)
	assert.Equal("ok", string(body))
}

func TestGetInvalidReplicas(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	_, err = proxy.Get(context.Background(), NewGetRequest("http://example.com/file.txt", WithGetRequestReplicas(0)))
	assert.ErrorIs(err, ErrInvalidArgument)
}

func TestGetIntoTimeout(t *testing.T) {
	assert := assert.New(t)

	proxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	})
	port := setupMockSeedPeer(t, nil)
	endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, proxyPort)})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	var buf bytes.Buffer
	req := NewGetRequest("http://example.com/file.txt", WithGetRequestReplicas(1), WithGetRequestTimeout(50*time.Millisecond))
	_, err = proxy.GetInto(context.Background(), req, &buf)
	assert.ErrorIs(err, ErrRequestTimeout)
}

func TestLookupEndpoints(t *testing.T) {
	assert := assert.New(t)
	endpoints := make(map[string]string, 3)
	var hosts []*commonv2.Host
	for _, name := range []string{"seed-peer-1", "seed-peer-2", "seed-peer-3"} {
		port := setupMockSeedPeer(t, nil)
		endpoints[name] = fmt.Sprintf("http://127.0.0.1:%d", port)
		hosts = append(hosts, createSeedPeerHost(name, port, 0))
	}
	endpoint := setupMockScheduler(t, hosts)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	blobURL := "http://registry.example.com/v2/library/ubuntu/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"
	tests := []struct {
		req   *GetRequest
		names []string
	}{
		{
			req: NewGetRequest(
				"https://example.com/file.txt?Expires=e1&Signature=s1&foo=bar",
				WithGetRequestPieceLength(4194304),
				WithGetRequestTag("tag-a"),
				WithGetRequestApplication("app-a"),
				WithGetRequestFilteredQueryParams([]string{"Expires", "Signature"}),
				WithGetRequestReplicas(3),
			),
			names: []string{"seed-peer-3", "seed-peer-2", "seed-peer-1"},
		},
		{
			req: NewGetRequest(
				"https://example.com/file.txt?Expires=e2&Signature=s2&foo=bar",
				WithGetRequestPieceLength(4194304),
				WithGetRequestTag("tag-a"),
				WithGetRequestApplication("app-a"),
				WithGetRequestFilteredQueryParams([]string{"Expires", "Signature"}),
				WithGetRequestReplicas(3),
			),
			names: []string{"seed-peer-3", "seed-peer-2", "seed-peer-1"},
		},
		{
			req:   NewGetRequest("https://example.com/file.txt"),
			names: []string{"seed-peer-1", "seed-peer-2"},
		},
		{
			req:   NewGetRequest("https://example.com/file.txt", WithGetRequestReplicas(1)),
			names: []string{"seed-peer-1"},
		},
		{
			req:   NewGetRequest("https://example.com/file.txt", WithGetRequestTag("tag-a")),
			names: []string{"seed-peer-3", "seed-peer-2"},
		},
		{
			req:   NewGetRequest("https://example.com/file.txt", WithGetRequestTag("tag-b")),
			names: []string{"seed-peer-2", "seed-peer-1"},
		},
		{
			req:   NewGetRequest("https://example.com/file.txt", WithGetRequestApplication("app-a")),
			names: []string{"seed-peer-1", "seed-peer-3"},
		},
		{
			req: NewGetRequest(
				"https://example.com/file.txt",
				WithGetRequestContentForCalculatingTaskID("This is a test file"),
				WithGetRequestReplicas(3),
			),
			names: []string{"seed-peer-2", "seed-peer-3", "seed-peer-1"},
		},
		{
			req:   NewGetRequest(blobURL, WithGetRequestReplicas(3)),
			names: []string{"seed-peer-3", "seed-peer-2", "seed-peer-1"},
		},
		{
			req:   NewGetRequest(blobURL, WithGetRequestEnableTaskIDBasedBlobDigest(false)),
			names: []string{"seed-peer-3", "seed-peer-1"},
		},
	}

	for _, tt := range tests {
		expected := make([]string, 0, len(tt.names))
		for _, name := range tt.names {
			expected = append(expected, endpoints[name])
		}

		addrs, err := proxy.LookupEndpoints(context.Background(), tt.req)
		assert.NoError(err)
		assert.Equal(expected, addrs)
	}
}

func TestLookupEndpointsNoAvailableSeedPeers(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	_, err = proxy.LookupEndpoints(context.Background(), NewGetRequest("http://example.com/payload.txt"))
	assert.ErrorIs(err, ErrInternal)
	assert.ErrorContains(err, "failed to select seed peers")
}

func TestTaskIDBlobDigestDisabled(t *testing.T) {
	assert := assert.New(t)

	blobURL := "http://registry.example.com/v2/library/ubuntu/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"
	id, err := generateTaskID(&GetRequest{url: blobURL})
	assert.NoError(err)
	assert.NotEqual("b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e", id)
}
