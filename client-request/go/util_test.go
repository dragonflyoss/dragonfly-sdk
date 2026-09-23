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
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUniqueURLs(t *testing.T) {
	tests := []struct {
		name   string
		urls   []string
		expect []string
	}{
		{
			name:   "empty",
			urls:   nil,
			expect: nil,
		},
		{
			name: "duplicate layers",
			urls: []string{
				"https://example.com/v2/foo/bar/blobs/sha256:cccc",
				"https://example.com/v2/foo/bar/blobs/sha256:bbbb",
				"https://example.com/v2/foo/bar/blobs/sha256:aaaa",
				"https://example.com/v2/foo/bar/blobs/sha256:bbbb",
				"https://example.com/v2/foo/bar/blobs/sha256:cccc",
			},
			expect: []string{
				"https://example.com/v2/foo/bar/blobs/sha256:aaaa",
				"https://example.com/v2/foo/bar/blobs/sha256:bbbb",
				"https://example.com/v2/foo/bar/blobs/sha256:cccc",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)
			assert.Equal(tc.expect, uniqueURLs(tc.urls))
		})
	}
}

func TestHeaderToMap(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
		expect map[string]string
	}{
		{
			name:   "empty",
			header: http.Header{},
			expect: map[string]string{},
		},
		{
			name:   "single value",
			header: http.Header{"Authorization": []string{"Bearer token"}},
			expect: map[string]string{"Authorization": "Bearer token"},
		},
		{
			name:   "last value wins",
			header: http.Header{"Accept": []string{"application/json", "application/vnd.oci.image.manifest.v1+json"}},
			expect: map[string]string{"Accept": "application/vnd.oci.image.manifest.v1+json"},
		},
		{
			name:   "skips empty values",
			header: http.Header{"X-Empty": nil, "X-Set": []string{"value"}},
			expect: map[string]string{"X-Set": "value"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)
			assert.Equal(tc.expect, headerToMap(tc.header))
		})
	}
}
