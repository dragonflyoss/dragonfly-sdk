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
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// httpRange specifies the byte range of a request or a response.
type httpRange struct {
	start, length int64
}

// String specifies the string of the Range header.
func (r httpRange) String() string {
	return fmt.Sprintf("bytes=%d-%d", r.start, r.start+r.length-1)
}

// contentRange specifies the byte range a response carries and the size of
// the whole resource.
type contentRange struct {
	httpRange httpRange
	size      int64
}

// parseContentRange parses a Content-Range header string as per RFC 9110. An
// error is returned if the range is unsatisfied or the size is unknown.
//
// Example:
//
//	"Content-Range": "bytes 100-200/1000"
func parseContentRange(s string) (contentRange, error) {
	const b = "bytes "
	if !strings.HasPrefix(s, b) {
		return contentRange{}, errors.New("invalid Content-Range")
	}

	ra, size, ok := strings.Cut(s[len(b):], "/")
	if !ok {
		return contentRange{}, errors.New("invalid Content-Range")
	}

	start, end, ok := strings.Cut(ra, "-")
	if !ok {
		return contentRange{}, errors.New("invalid Content-Range")
	}

	// Parse the numbers as 63-bit unsigned integers like the Content-Length
	// of net/http, so they are digits alone and fit in an int64.
	i, err := strconv.ParseUint(start, 10, 63)
	if err != nil {
		return contentRange{}, errors.New("invalid Content-Range")
	}

	j, err := strconv.ParseUint(end, 10, 63)
	if err != nil {
		return contentRange{}, errors.New("invalid Content-Range")
	}

	n, err := strconv.ParseUint(size, 10, 63)
	if err != nil {
		return contentRange{}, errors.New("invalid Content-Range")
	}

	if i > j || j >= n {
		return contentRange{}, errors.New("invalid Content-Range")
	}

	return contentRange{
		httpRange: httpRange{start: int64(i), length: int64(j - i + 1)},
		size:      int64(n),
	}, nil
}
