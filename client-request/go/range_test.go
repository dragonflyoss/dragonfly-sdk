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
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHTTPRangeString(t *testing.T) {
	tests := []struct {
		name   string
		r      httpRange
		expect func(t *testing.T, s string)
	}{
		{
			name: "one byte",
			r:    httpRange{start: 0, length: 1},
			expect: func(t *testing.T, s string) {
				assert.Equal(t, "bytes=0-0", s)
			},
		},
		{
			name: "a range",
			r:    httpRange{start: 2, length: 4},
			expect: func(t *testing.T, s string) {
				assert.Equal(t, "bytes=2-5", s)
			},
		},
		{
			name: "the bytes left of a range",
			r:    httpRange{start: 600000, length: 448576},
			expect: func(t *testing.T, s string) {
				assert.Equal(t, "bytes=600000-1048575", s)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.expect(t, tc.r.String())
		})
	}
}

func TestParseContentRange(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		expect func(t *testing.T, c contentRange, err error)
	}{
		{
			name: "a range",
			s:    "bytes 2-5/8",
			expect: func(t *testing.T, c contentRange, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal(contentRange{httpRange: httpRange{start: 2, length: 4}, size: 8}, c)
			},
		},
		{
			name: "the whole representation",
			s:    "bytes 0-7/8",
			expect: func(t *testing.T, c contentRange, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal(contentRange{httpRange: httpRange{start: 0, length: 8}, size: 8}, c)
			},
		},
		{
			name: "one byte",
			s:    "bytes 0-0/1",
			expect: func(t *testing.T, c contentRange, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal(contentRange{httpRange: httpRange{start: 0, length: 1}, size: 1}, c)
			},
		},
		{
			name: "the largest positions",
			s:    "bytes 9223372036854775806-9223372036854775806/9223372036854775807",
			expect: func(t *testing.T, c contentRange, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal(contentRange{httpRange: httpRange{start: math.MaxInt64 - 1, length: 1}, size: math.MaxInt64}, c)
			},
		},
		{
			name: "unknown size",
			s:    "bytes 2-5/*",
			expect: func(t *testing.T, c contentRange, err error) {
				assert.EqualError(t, err, "invalid Content-Range")
			},
		},
		{
			name: "unsatisfied range",
			s:    "bytes */8",
			expect: func(t *testing.T, c contentRange, err error) {
				assert.EqualError(t, err, "invalid Content-Range")
			},
		},
		{
			name: "another unit",
			s:    "items 2-5/8",
			expect: func(t *testing.T, c contentRange, err error) {
				assert.EqualError(t, err, "invalid Content-Range")
			},
		},
		{
			name: "no last position",
			s:    "bytes 2/8",
			expect: func(t *testing.T, c contentRange, err error) {
				assert.EqualError(t, err, "invalid Content-Range")
			},
		},
		{
			name: "first position not a number",
			s:    "bytes x-5/8",
			expect: func(t *testing.T, c contentRange, err error) {
				assert.EqualError(t, err, "invalid Content-Range")
			},
		},
		{
			name: "last position not a number",
			s:    "bytes 2-x/8",
			expect: func(t *testing.T, c contentRange, err error) {
				assert.EqualError(t, err, "invalid Content-Range")
			},
		},
		{
			name: "size not a number",
			s:    "bytes 2-5/x",
			expect: func(t *testing.T, c contentRange, err error) {
				assert.EqualError(t, err, "invalid Content-Range")
			},
		},
		{
			name: "signed first position",
			s:    "bytes +2-5/8",
			expect: func(t *testing.T, c contentRange, err error) {
				assert.EqualError(t, err, "invalid Content-Range")
			},
		},
		{
			name: "signed size",
			s:    "bytes 2-5/+8",
			expect: func(t *testing.T, c contentRange, err error) {
				assert.EqualError(t, err, "invalid Content-Range")
			},
		},
		{
			name: "two sizes",
			s:    "bytes 2-5/8/9",
			expect: func(t *testing.T, c contentRange, err error) {
				assert.EqualError(t, err, "invalid Content-Range")
			},
		},
		{
			name: "last position below the first",
			s:    "bytes 5-2/8",
			expect: func(t *testing.T, c contentRange, err error) {
				assert.EqualError(t, err, "invalid Content-Range")
			},
		},
		{
			name: "size not above the last position",
			s:    "bytes 2-5/5",
			expect: func(t *testing.T, c contentRange, err error) {
				assert.EqualError(t, err, "invalid Content-Range")
			},
		},
		{
			name: "empty resource",
			s:    "bytes 2-5/0",
			expect: func(t *testing.T, c contentRange, err error) {
				assert.EqualError(t, err, "invalid Content-Range")
			},
		},
		{
			name: "size above the largest int64",
			s:    "bytes 2-5/9223372036854775808",
			expect: func(t *testing.T, c contentRange, err error) {
				assert.EqualError(t, err, "invalid Content-Range")
			},
		},
		{
			name: "leading space",
			s:    " bytes 2-5/8",
			expect: func(t *testing.T, c contentRange, err error) {
				assert.EqualError(t, err, "invalid Content-Range")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := parseContentRange(tc.s)
			tc.expect(t, c, err)
		})
	}
}
