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
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
)

type mockSender struct {
	responses []*http.Response
	errs      []error
	ranges    []string
	headers   []http.Header
}

func (s *mockSender) sendTo(_ context.Context, _ string, req *GetRequest) (*http.Response, error) {
	s.ranges = append(s.ranges, req.header.Get("Range"))
	s.headers = append(s.headers, req.header.Clone())
	resp, err := s.responses[0], s.errs[0]
	s.responses, s.errs = s.responses[1:], s.errs[1:]
	return resp, err
}

func TestResumableBodyRead(t *testing.T) {
	type answer struct {
		statusCode    int
		contentRange  string
		contentLength int64
		body          string
		bodyErr       error
		err           error
	}

	tests := []struct {
		name       string
		maxRetries uint8
		answers    []answer
		expect     func(t *testing.T, content []byte, ranges []string, err error)
	}{
		{
			name:       "reads a body that does not break off",
			maxRetries: 1,
			answers: []answer{
				{statusCode: http.StatusPartialContent, contentRange: "bytes 0-14/15", body: "hello dragonfly"},
			},
			expect: func(t *testing.T, content []byte, ranges []string, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal("hello dragonfly", string(content))
				assert.Empty(ranges)
			},
		},
		{
			name:       "resumes a body that breaks off",
			maxRetries: 1,
			answers: []answer{
				{statusCode: http.StatusPartialContent, contentRange: "bytes 0-14/15", body: "hello ", bodyErr: io.ErrUnexpectedEOF},
				{statusCode: http.StatusPartialContent, contentRange: "bytes 6-14/15", body: "dragonfly"},
			},
			expect: func(t *testing.T, content []byte, ranges []string, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal("hello dragonfly", string(content))
				assert.Equal([]string{"bytes=6-14"}, ranges)
			},
		},
		{
			name:       "resumes a whole body by its content length",
			maxRetries: 1,
			answers: []answer{
				{statusCode: http.StatusOK, contentLength: 15, body: "hello ", bodyErr: io.ErrUnexpectedEOF},
				{statusCode: http.StatusPartialContent, contentRange: "bytes 6-14/15", body: "dragonfly"},
			},
			expect: func(t *testing.T, content []byte, ranges []string, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal("hello dragonfly", string(content))
				assert.Equal([]string{"bytes=6-14"}, ranges)
			},
		},
		{
			name:       "resumes a body that ends short",
			maxRetries: 1,
			answers: []answer{
				{statusCode: http.StatusPartialContent, contentRange: "bytes 0-14/15", body: "hello "},
				{statusCode: http.StatusPartialContent, contentRange: "bytes 6-14/15", body: "dragonfly"},
			},
			expect: func(t *testing.T, content []byte, ranges []string, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal("hello dragonfly", string(content))
				assert.Equal([]string{"bytes=6-14"}, ranges)
			},
		},
		{
			name:       "resumes a body that times out",
			maxRetries: 1,
			answers: []answer{
				{statusCode: http.StatusPartialContent, contentRange: "bytes 0-14/15", body: "hello ", bodyErr: context.DeadlineExceeded},
				{statusCode: http.StatusPartialContent, contentRange: "bytes 6-14/15", body: "dragonfly"},
			},
			expect: func(t *testing.T, content []byte, ranges []string, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal("hello dragonfly", string(content))
				assert.Equal([]string{"bytes=6-14"}, ranges)
			},
		},
		{
			name:       "resumes inside the range of the request",
			maxRetries: 1,
			answers: []answer{
				{statusCode: http.StatusPartialContent, contentRange: "bytes 100-114/1000", body: "hello ", bodyErr: io.ErrUnexpectedEOF},
				{statusCode: http.StatusPartialContent, contentRange: "bytes 106-114/1000", body: "dragonfly"},
			},
			expect: func(t *testing.T, content []byte, ranges []string, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal("hello dragonfly", string(content))
				assert.Equal([]string{"bytes=106-114"}, ranges)
			},
		},
		{
			name:       "resumes a body that breaks off twice",
			maxRetries: 2,
			answers: []answer{
				{statusCode: http.StatusPartialContent, contentRange: "bytes 0-14/15", body: "hello ", bodyErr: io.ErrUnexpectedEOF},
				{statusCode: http.StatusPartialContent, contentRange: "bytes 6-14/15", body: "drag", bodyErr: io.ErrUnexpectedEOF},
				{statusCode: http.StatusPartialContent, contentRange: "bytes 10-14/15", body: "onfly"},
			},
			expect: func(t *testing.T, content []byte, ranges []string, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal("hello dragonfly", string(content))
				assert.Equal([]string{"bytes=6-14", "bytes=10-14"}, ranges)
			},
		},
		{
			name:       "sends the request again after a response of other bytes",
			maxRetries: 2,
			answers: []answer{
				{statusCode: http.StatusPartialContent, contentRange: "bytes 0-14/15", body: "hello ", bodyErr: io.ErrUnexpectedEOF},
				{statusCode: http.StatusPartialContent, contentRange: "bytes 0-14/15", body: "hello dragonfly"},
				{statusCode: http.StatusPartialContent, contentRange: "bytes 6-14/15", body: "dragonfly"},
			},
			expect: func(t *testing.T, content []byte, ranges []string, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal("hello dragonfly", string(content))
				assert.Equal([]string{"bytes=6-14", "bytes=6-14"}, ranges)
			},
		},
		{
			name:       "fails without a retry left",
			maxRetries: 0,
			answers: []answer{
				{statusCode: http.StatusPartialContent, contentRange: "bytes 0-14/15", body: "hello ", bodyErr: io.ErrUnexpectedEOF},
			},
			expect: func(t *testing.T, content []byte, ranges []string, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, ErrInternal)
				assert.ErrorIs(err, io.ErrUnexpectedEOF)
				assert.Equal("hello ", string(content))
				assert.Empty(ranges)
			},
		},
		{
			name:       "fails with a timeout without a retry left",
			maxRetries: 0,
			answers: []answer{
				{statusCode: http.StatusPartialContent, contentRange: "bytes 0-14/15", body: "hello ", bodyErr: context.DeadlineExceeded},
			},
			expect: func(t *testing.T, content []byte, ranges []string, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, ErrRequestTimeout)
				assert.Empty(ranges)
			},
		},
		{
			name:       "fails without a known length",
			maxRetries: 1,
			answers: []answer{
				{statusCode: http.StatusOK, contentLength: -1, body: "hello ", bodyErr: io.ErrUnexpectedEOF},
			},
			expect: func(t *testing.T, content []byte, ranges []string, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, ErrInternal)
				assert.Empty(ranges)
			},
		},
		{
			name:       "fails on a response of another range",
			maxRetries: 1,
			answers: []answer{
				{statusCode: http.StatusPartialContent, contentRange: "bytes 0-14/15", body: "hello ", bodyErr: io.ErrUnexpectedEOF},
				{statusCode: http.StatusPartialContent, contentRange: "bytes 0-14/15", body: "hello dragonfly"},
			},
			expect: func(t *testing.T, content []byte, ranges []string, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, ErrInternal)
				assert.ErrorContains(err, "response sent again does not resume the body")
				assert.Equal([]string{"bytes=6-14"}, ranges)
			},
		},
		{
			name:       "fails on a response of another complete length",
			maxRetries: 1,
			answers: []answer{
				{statusCode: http.StatusPartialContent, contentRange: "bytes 0-14/15", body: "hello ", bodyErr: io.ErrUnexpectedEOF},
				{statusCode: http.StatusPartialContent, contentRange: "bytes 6-14/16", body: "dragonfly!"},
			},
			expect: func(t *testing.T, content []byte, ranges []string, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, ErrInternal)
				assert.ErrorContains(err, "response sent again does not resume the body")
				assert.Equal([]string{"bytes=6-14"}, ranges)
			},
		},
		{
			name:       "fails on a definitive answer",
			maxRetries: 1,
			answers: []answer{
				{statusCode: http.StatusPartialContent, contentRange: "bytes 0-14/15", body: "hello ", bodyErr: io.ErrUnexpectedEOF},
				{err: &BackendError{StatusCode: http.StatusNotFound}},
			},
			expect: func(t *testing.T, content []byte, ranges []string, err error) {
				assert := assert.New(t)
				var backendErr *BackendError
				assert.ErrorAs(err, &backendErr)
				assert.Equal(http.StatusNotFound, backendErr.StatusCode)
				assert.Equal([]string{"bytes=6-14"}, ranges)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sender := &mockSender{}
			for _, answer := range tc.answers {
				header := make(http.Header)
				if answer.contentRange != "" {
					header.Set("Content-Range", answer.contentRange)
				}

				bodyErr := answer.bodyErr
				if bodyErr == nil {
					bodyErr = io.EOF
				}

				sender.responses = append(sender.responses, &http.Response{
					StatusCode:    answer.statusCode,
					Header:        header,
					ContentLength: answer.contentLength,
					Body:          io.NopCloser(io.MultiReader(strings.NewReader(answer.body), iotest.ErrReader(bodyErr))),
				})
				sender.errs = append(sender.errs, answer.err)
			}

			resp := sender.responses[0]
			sender.responses, sender.errs = sender.responses[1:], sender.errs[1:]
			attempts, err := retryPolicy{maxRetries: tc.maxRetries}.newAttempts([]string{"a", "b"}, time.Second)
			assert.NoError(t, err)

			body := newResumableBody(context.Background(), sender, NewGetRequest("http://example.com/file.txt"), attempts, resp, func() {})
			content, err := io.ReadAll(body)
			assert.NoError(t, body.Close())
			tc.expect(t, content, sender.ranges, err)
		})
	}
}

func TestResumableBodyReadAfterTheBodyEnded(t *testing.T) {
	tests := []struct {
		name      string
		closeBody bool
		expect    func(t *testing.T, first, second error, ranges []string, cancels int)
	}{
		{
			name: "after a reopen failed",
			expect: func(t *testing.T, first, second error, ranges []string, cancels int) {
				assert := assert.New(t)
				var backendErr *BackendError
				assert.ErrorAs(first, &backendErr)
				assert.Equal(first, second)
				assert.Equal([]string{"bytes=6-14"}, ranges)
				assert.Positive(cancels)
			},
		},
		{
			name:      "after Close",
			closeBody: true,
			expect: func(t *testing.T, first, second error, ranges []string, cancels int) {
				assert := assert.New(t)
				assert.ErrorIs(first, http.ErrBodyReadAfterClose)
				assert.Equal(first, second)
				assert.Empty(ranges)
				assert.Positive(cancels)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sender := &mockSender{
				responses: []*http.Response{nil, {
					StatusCode: http.StatusPartialContent,
					Header:     http.Header{"Content-Range": {"bytes 6-14/15"}},
					Body:       io.NopCloser(strings.NewReader("dragonfly")),
				}},
				errs: []error{&BackendError{StatusCode: http.StatusNotFound}, nil},
			}
			resp := &http.Response{
				StatusCode: http.StatusPartialContent,
				Header:     http.Header{"Content-Range": {"bytes 0-14/15"}},
				Body:       io.NopCloser(io.MultiReader(strings.NewReader("hello "), iotest.ErrReader(io.ErrUnexpectedEOF))),
			}
			attempts, err := retryPolicy{maxRetries: 3}.newAttempts([]string{"a", "b"}, time.Second)
			assert.NoError(t, err)

			cancels := 0
			body := newResumableBody(context.Background(), sender, NewGetRequest("http://example.com/file.txt"), attempts, resp, func() { cancels++ })
			_, err = io.ReadFull(body, make([]byte, 5))
			assert.NoError(t, err)
			if tc.closeBody {
				assert.NoError(t, body.Close())
			}

			_, first := io.ReadAll(body)
			_, second := io.ReadAll(body)
			tc.expect(t, first, second, sender.ranges, cancels)
		})
	}
}

func TestResumableBodyOwnsTheRequestHeader(t *testing.T) {
	assert := assert.New(t)

	sender := &mockSender{
		responses: []*http.Response{{
			StatusCode: http.StatusPartialContent,
			Header:     http.Header{"Content-Range": {"bytes 6-14/15"}},
			Body:       io.NopCloser(strings.NewReader("dragonfly")),
		}},
		errs: []error{nil},
	}
	resp := &http.Response{
		StatusCode: http.StatusPartialContent,
		Header:     http.Header{"Content-Range": {"bytes 0-14/15"}},
		Body:       io.NopCloser(io.MultiReader(strings.NewReader("hello "), iotest.ErrReader(io.ErrUnexpectedEOF))),
	}
	attempts, err := retryPolicy{maxRetries: 1}.newAttempts([]string{"a"}, time.Second)
	assert.NoError(err)

	header := http.Header{"X-Token": {"a"}}
	req := NewGetRequest("http://example.com/file.txt", WithGetRequestHeader(header))
	body := newResumableBody(context.Background(), sender, req, attempts, resp, func() {})
	header.Set("X-Token", "b")

	content, err := io.ReadAll(body)
	assert.NoError(err)
	assert.Equal("hello dragonfly", string(content))
	assert.Equal("a", sender.headers[0].Get("X-Token"))
	assert.Equal("bytes=6-14", sender.headers[0].Get("Range"))
	assert.Empty(header.Get("Range"))
}
