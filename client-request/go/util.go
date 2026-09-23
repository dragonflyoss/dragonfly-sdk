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
	"slices"
)

// uniqueURLs sorts the urls and removes the duplicates, so a blob referenced
// from several layers or platform manifests yields one task.
func uniqueURLs(urls []string) []string {
	slices.Sort(urls)
	return slices.Compact(urls)
}

// headerToMap converts an http.Header to a map, the last value wins for
// duplicate keys.
func headerToMap(header http.Header) map[string]string {
	m := make(map[string]string, len(header))
	for k, v := range header {
		if len(v) > 0 {
			m[k] = v[len(v)-1]
		}
	}

	return m
}
