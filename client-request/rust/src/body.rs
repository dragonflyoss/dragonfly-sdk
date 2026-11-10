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

//! The streaming body of a GET response, sent again on the next seed peer for
//! the bytes left when it breaks off.
//!
//! `get_into` retries a failed body read by reading the body again from the
//! start. A streaming body has already handed out the bytes it read, so it
//! sends the request again within the same retries, its `Range` narrowed to the
//! bytes left:
//!
//! ```text
//! attempt 1 ──▶ B ──▶ 206 bytes 0-14/15 ──▶ chunk ──▶ broke off at byte 6 ── sleep(delay)
//! attempt 2 ──▶ A ──▶ Range: bytes=6-14
//!                     ├─ 206 bytes 6-14/15 ──▶ chunk ──▶ ... ──▶ end
//!                     └─ any other answer ──▶ sleep(delay) ──▶ attempt 3 ──▶ B
//! ```
//!
//! Like containerd and rclone, a response sent again is matched by its
//! `Content-Range` alone, which must name exactly the bytes left of the same
//! complete length. No validator such as an ETag is compared: upstreams often
//! send none, and every seed peer stores the headers of its own origin request.
//! The bytes are left to the integrity checks of the caller, such as a digest.

use crate::errors::Error;
use crate::range::{ContentRange, Range};
use crate::retry::{Attempts, RetryPolicy};
use crate::{Body, Result};
use async_trait::async_trait;
use bytes::Bytes;
use futures::stream;
use reqwest::header::{HeaderValue, CONTENT_LENGTH, CONTENT_RANGE, RANGE};
use reqwest::{Request, Response, StatusCode};
use tracing::warn;

/// The interface for sending a request once to the seed peer proxy at an
/// endpoint.
///
/// Implemented by the proxy clients, so a body is sent again through the
/// client that sent it first.
#[async_trait]
pub(crate) trait Sender: Send + Sync + 'static {
    /// Sends a copy of `request` once to the seed peer proxy at `endpoint`.
    async fn send_to(&self, endpoint: String, request: &Request) -> Result<Response>;
}

/// The streaming body of a response that sends its request again on the next
/// seed peer for the bytes left when it breaks off.
pub(crate) struct ResumableBody<S> {
    /// The client that sent the request, sending it again on every reopen.
    sender: S,

    /// The request, its `Range` narrowed to the bytes left on every reopen.
    request: Request,

    /// The attempts of the request, shared with the one that sent it.
    attempts: Attempts,

    /// The bytes the first response carries, `None` when it does not tell them
    /// and the body cannot be reopened.
    content_range: Option<ContentRange>,

    /// The bytes handed out so far.
    offset: u64,

    /// The current response, its body read chunk by chunk.
    response: Response,
}

/// Implements the resumable body.
impl<S: Sender> ResumableBody<S> {
    /// Creates the body of `response` to `request`, sent again through
    /// `sender` within `attempts` when it breaks off.
    pub(crate) fn new(sender: S, response: Response, request: Request, attempts: Attempts) -> Self {
        Self {
            sender,
            request,
            attempts,
            content_range: Self::content_range(&response),
            offset: 0,
            response,
        }
    }

    /// Converts into the streaming body of a response, the state boxed so that
    /// handing out a chunk moves a pointer.
    pub(crate) fn into_body(self) -> Body {
        Box::new(Box::pin(stream::try_unfold(Box::new(self), Self::next)))
    }

    /// Hands out the next chunk, reopening the body when it breaks off.
    async fn next(mut self: Box<Self>) -> Result<Option<(Bytes, Box<Self>)>> {
        loop {
            let err = match self.response.chunk().await {
                Ok(Some(chunk)) => {
                    self.offset += chunk.len() as u64;
                    return Ok(Some((chunk, self)));
                }
                Ok(None) => match self.remaining() {
                    Some(remaining) => Error::Internal(format!(
                        "response body ended without {}",
                        Range(remaining.range)
                    )),
                    None => return Ok(None),
                },
                Err(err) if err.is_timeout() => Error::RequestTimeout(err.to_string()),
                Err(err) => Error::Internal(format!("failed to read response body: {err}")),
            };

            // Box the reopen, so the future of every chunk stays small.
            Box::pin(self.reopen(err)).await?;
        }
    }

    /// Sends the request again for the bytes left on the next endpoints within
    /// the retries left, failing with `err` when no byte or retry is left.
    async fn reopen(&mut self, err: Error) -> Result<()> {
        let Some(remaining) = self.remaining() else {
            return Err(err);
        };

        let Some(delay) = self.attempts.backoff() else {
            return Err(err);
        };

        warn!(
            "response body broke off at byte {} of {}, sending it again in {:?}: {}",
            remaining.range.start(),
            remaining.complete_length,
            delay,
            err
        );
        RetryPolicy::sleep(delay).await;

        // Narrow the range of the request to the bytes left.
        let range = Range(remaining.range.clone());
        self.request.headers_mut().insert(
            RANGE,
            HeaderValue::try_from(range.to_string())
                .map_err(|err| Error::Internal(err.to_string()))?,
        );

        // Send the request again until a response resumes the body, a response
        // of other bytes failing like an unreachable seed peer.
        let sender = &self.sender;
        let request = &self.request;
        let remaining = &remaining;
        let ((), result) = self
            .attempts
            .run((), |(), endpoint| async move {
                let result = sender
                    .send_to(endpoint, request)
                    .await
                    .and_then(|response| {
                        if Self::content_range(&response).as_ref() != Some(remaining) {
                            return Err(Error::Internal(
                                "response sent again does not resume the body".to_string(),
                            ));
                        }

                        Ok(response)
                    });
                ((), result)
            })
            .await;

        self.response = result?;
        Ok(())
    }

    /// Returns the bytes left of the ones the first response carries, `None`
    /// when it does not tell them or none is left.
    fn remaining(&self) -> Option<ContentRange> {
        let content_range = self.content_range.as_ref()?;
        let range =
            content_range.range.start().saturating_add(self.offset)..=*content_range.range.end();
        (!range.is_empty()).then_some(ContentRange {
            range,
            complete_length: content_range.complete_length,
        })
    }

    /// Returns the bytes `response` carries: the `Content-Range` of a `206`, the
    /// whole `Content-Length` of a `200`, `None` otherwise.
    fn content_range(response: &Response) -> Option<ContentRange> {
        let header = |name| response.headers().get(name)?.to_str().ok();
        match response.status() {
            StatusCode::PARTIAL_CONTENT => header(CONTENT_RANGE)?.parse().ok(),
            StatusCode::OK => {
                let complete_length: u64 = header(CONTENT_LENGTH)?.parse().ok()?;
                Some(ContentRange {
                    range: 0..=complete_length.checked_sub(1)?,
                    complete_length,
                })
            }
            _ => None,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use futures::TryStreamExt;
    use reqwest::header::{HeaderName, CONTENT_TYPE};
    use reqwest::Method;
    use std::collections::VecDeque;
    use std::sync::{Arc, Mutex};

    type Chunk = std::result::Result<&'static str, &'static str>;
    type Answer = (u16, HeaderName, &'static str, Vec<Chunk>);
    type ExpectBody = fn(Result<Vec<u8>>, Vec<String>);

    struct MockSender {
        answers: Mutex<VecDeque<Result<Response>>>,
        ranges: Arc<Mutex<Vec<String>>>,
    }

    #[async_trait]
    impl Sender for MockSender {
        async fn send_to(&self, _endpoint: String, request: &Request) -> Result<Response> {
            let range = request.headers().get(RANGE).unwrap().to_str().unwrap();
            self.ranges.lock().unwrap().push(range.to_string());
            self.answers.lock().unwrap().pop_front().unwrap()
        }
    }

    #[tokio::test]
    async fn resumable_body_sends_the_request_again_for_the_bytes_left() {
        let test_cases: Vec<(u8, Vec<Result<Answer>>, ExpectBody)> = vec![
            (
                1,
                vec![Ok((
                    206,
                    CONTENT_RANGE,
                    "bytes 0-14/15",
                    vec![Ok("hello "), Ok("dragonfly")],
                ))],
                |result, ranges| {
                    assert_eq!(result.unwrap(), b"hello dragonfly");
                    assert!(ranges.is_empty());
                },
            ),
            (
                1,
                vec![
                    Ok((
                        206,
                        CONTENT_RANGE,
                        "bytes 0-14/15",
                        vec![Ok("hello "), Err("reset")],
                    )),
                    Ok((
                        206,
                        CONTENT_RANGE,
                        "bytes 6-14/15",
                        vec![Ok("drag"), Ok("onfly")],
                    )),
                ],
                |result, ranges| {
                    assert_eq!(result.unwrap(), b"hello dragonfly");
                    assert_eq!(ranges, ["bytes=6-14"]);
                },
            ),
            (
                1,
                vec![
                    Ok((200, CONTENT_LENGTH, "15", vec![Ok("hello "), Err("reset")])),
                    Ok((206, CONTENT_RANGE, "bytes 6-14/15", vec![Ok("dragonfly")])),
                ],
                |result, ranges| {
                    assert_eq!(result.unwrap(), b"hello dragonfly");
                    assert_eq!(ranges, ["bytes=6-14"]);
                },
            ),
            (
                1,
                vec![
                    Ok((206, CONTENT_RANGE, "bytes 0-14/15", vec![Ok("hello ")])),
                    Ok((206, CONTENT_RANGE, "bytes 6-14/15", vec![Ok("dragonfly")])),
                ],
                |result, ranges| {
                    assert_eq!(result.unwrap(), b"hello dragonfly");
                    assert_eq!(ranges, ["bytes=6-14"]);
                },
            ),
            (
                1,
                vec![
                    Ok((
                        206,
                        CONTENT_RANGE,
                        "bytes 100-114/1000",
                        vec![Ok("hello "), Err("reset")],
                    )),
                    Ok((
                        206,
                        CONTENT_RANGE,
                        "bytes 106-114/1000",
                        vec![Ok("dragonfly")],
                    )),
                ],
                |result, ranges| {
                    assert_eq!(result.unwrap(), b"hello dragonfly");
                    assert_eq!(ranges, ["bytes=106-114"]);
                },
            ),
            (
                2,
                vec![
                    Ok((
                        206,
                        CONTENT_RANGE,
                        "bytes 0-14/15",
                        vec![Ok("hello "), Err("reset")],
                    )),
                    Ok((
                        206,
                        CONTENT_RANGE,
                        "bytes 6-14/15",
                        vec![Ok("drag"), Err("reset")],
                    )),
                    Ok((206, CONTENT_RANGE, "bytes 10-14/15", vec![Ok("onfly")])),
                ],
                |result, ranges| {
                    assert_eq!(result.unwrap(), b"hello dragonfly");
                    assert_eq!(ranges, ["bytes=6-14", "bytes=10-14"]);
                },
            ),
            (
                2,
                vec![
                    Ok((
                        206,
                        CONTENT_RANGE,
                        "bytes 0-14/15",
                        vec![Ok("hello "), Err("reset")],
                    )),
                    Ok((
                        206,
                        CONTENT_RANGE,
                        "bytes 0-14/15",
                        vec![Ok("hello dragonfly")],
                    )),
                    Ok((206, CONTENT_RANGE, "bytes 6-14/15", vec![Ok("dragonfly")])),
                ],
                |result, ranges| {
                    assert_eq!(result.unwrap(), b"hello dragonfly");
                    assert_eq!(ranges, ["bytes=6-14", "bytes=6-14"]);
                },
            ),
            (
                0,
                vec![Ok((
                    206,
                    CONTENT_RANGE,
                    "bytes 0-14/15",
                    vec![Ok("hello "), Err("reset")],
                ))],
                |result, ranges| {
                    assert!(
                        matches!(result, Err(Error::Internal(_))),
                        "result: {result:?}"
                    );
                    assert!(ranges.is_empty());
                },
            ),
            (
                1,
                vec![Ok((
                    200,
                    CONTENT_TYPE,
                    "text/plain",
                    vec![Ok("hello "), Err("reset")],
                ))],
                |result, ranges| {
                    assert!(
                        matches!(result, Err(Error::Internal(_))),
                        "result: {result:?}"
                    );
                    assert!(ranges.is_empty());
                },
            ),
            (
                1,
                vec![
                    Ok((
                        206,
                        CONTENT_RANGE,
                        "bytes 0-14/15",
                        vec![Ok("hello "), Err("reset")],
                    )),
                    Ok((
                        206,
                        CONTENT_RANGE,
                        "bytes 0-14/15",
                        vec![Ok("hello dragonfly")],
                    )),
                ],
                |result, ranges| {
                    assert_eq!(
                        result.unwrap_err().to_string(),
                        "request internal error: response sent again does not resume the body"
                    );
                    assert_eq!(ranges, ["bytes=6-14"]);
                },
            ),
            (
                1,
                vec![
                    Ok((
                        206,
                        CONTENT_RANGE,
                        "bytes 0-14/15",
                        vec![Ok("hello "), Err("reset")],
                    )),
                    Ok((206, CONTENT_RANGE, "bytes 6-14/16", vec![Ok("dragonfly!")])),
                ],
                |result, ranges| {
                    assert_eq!(
                        result.unwrap_err().to_string(),
                        "request internal error: response sent again does not resume the body"
                    );
                    assert_eq!(ranges, ["bytes=6-14"]);
                },
            ),
            (
                1,
                vec![
                    Ok((
                        206,
                        CONTENT_RANGE,
                        "bytes 0-14/15",
                        vec![Ok("hello "), Err("reset")],
                    )),
                    Err(Error::InvalidArgument("bad".to_string())),
                ],
                |result, ranges| {
                    assert!(
                        matches!(result, Err(Error::InvalidArgument(_))),
                        "result: {result:?}"
                    );
                    assert_eq!(ranges, ["bytes=6-14"]);
                },
            ),
        ];

        for (max_retries, answers, expect) in test_cases {
            let policy = RetryPolicy {
                max_retries,
                backoff: None,
            };
            let attempts = policy
                .attempts(Arc::from(["a".to_string(), "b".to_string()]))
                .unwrap();
            let request = Request::new(Method::GET, "http://example.com/blob".parse().unwrap());
            let ranges = Arc::new(Mutex::new(Vec::new()));
            let mut answers: VecDeque<Result<Response>> = answers
                .into_iter()
                .map(|answer| {
                    answer.map(|(status, name, value, chunks)| {
                        let chunks = chunks.into_iter().map(|chunk| {
                            chunk
                                .map(|chunk| Bytes::from_static(chunk.as_bytes()))
                                .map_err(std::io::Error::other)
                        });

                        ::http::Response::builder()
                            .status(status)
                            .header(name, value)
                            .body(reqwest::Body::wrap_stream(stream::iter(chunks)))
                            .unwrap()
                            .into()
                    })
                })
                .collect();
            let response = answers.pop_front().unwrap().unwrap();
            let sender = MockSender {
                answers: Mutex::new(answers),
                ranges: ranges.clone(),
            };
            let body = ResumableBody::new(sender, response, request, attempts).into_body();

            let result = body
                .try_fold(Vec::new(), |mut content, chunk| async move {
                    content.extend_from_slice(&chunk);
                    Ok(content)
                })
                .await;
            let ranges = ranges.lock().unwrap().clone();
            expect(result, ranges);
        }
    }
}
