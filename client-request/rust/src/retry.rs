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

use crate::errors::Error;
use crate::Result;
use backon::{Backoff, BackoffBuilder, ExponentialBuilder, Retryable, RetryableWithContext};
use rand::seq::SliceRandom;
use std::future::Future;
use std::time::Duration;

/// The retry policy of the requests of a client.
#[derive(Debug, Clone, Copy)]
pub(crate) struct RetryPolicy {
    /// The maximum number of retries of a request on a transient failure, each sent
    /// to the next seed peer.
    pub(crate) max_retries: u8,

    /// The exponential backoff between the retries, none retrying at once.
    pub(crate) backoff: Option<ExponentialBuilder>,
}

/// Implements Default trait.
impl Default for RetryPolicy {
    /// Returns a policy retrying once at once.
    fn default() -> Self {
        Self {
            max_retries: 1,
            backoff: None,
        }
    }
}

/// Implements the retry policy.
impl RetryPolicy {
    /// Returns the delays before each of the `max_retries` retries: the exponential
    /// sequence of the backoff, or zero delays when no backoff is set.
    fn delays(&self) -> Box<dyn Backoff> {
        match self.backoff {
            Some(backoff) => Box::new(backoff.with_max_times(self.max_retries as usize).build()),
            None => Box::new(std::iter::repeat_n(
                Duration::ZERO,
                self.max_retries as usize,
            )),
        }
    }
}

/// Sleeps for the delay, returning at once for a zero delay so retries without a
/// backoff never yield to the timer.
async fn sleep(delay: Duration) {
    if !delay.is_zero() {
        tokio::time::sleep(delay).await;
    }
}

/// Retries a failure the policy deems transient up to its max retries after the
/// backoff delay. A definitive failure returns at once.
pub(crate) async fn retry<T, F, Fut>(policy: RetryPolicy, attempt: F) -> Result<T>
where
    F: FnMut() -> Fut,
    Fut: Future<Output = Result<T>>,
{
    attempt
        .retry(policy.delays())
        .sleep(sleep)
        .when(Error::is_retryable)
        .await
}

/// Scatters a request across the endpoints: each attempt targets the next endpoint
/// of a shuffled order, wrapping around when the endpoints are fewer than the
/// attempts, and a failure the policy deems transient is retried up to its max
/// retries after the backoff delay. A definitive failure returns at once. The
/// context is handed to every attempt and back to the caller, so a buffer can
/// survive across attempts.
pub(crate) async fn retry_with_endpoints<Ctx, T, F, Fut>(
    policy: RetryPolicy,
    endpoints: &[String],
    ctx: Ctx,
    mut attempt: F,
) -> (Ctx, Result<T>)
where
    F: FnMut(Ctx, String) -> Fut,
    Fut: Future<Output = (Ctx, Result<T>)>,
{
    if endpoints.is_empty() {
        return (
            ctx,
            Err(Error::InvalidArgument(
                "no endpoints to send request".to_string(),
            )),
        );
    }

    let mut shuffled: Vec<&String> = endpoints.iter().collect();
    shuffled.shuffle(&mut rand::rng());

    let mut attempts = 0usize;
    (|ctx: Ctx| {
        let endpoint = shuffled[attempts % shuffled.len()].clone();
        attempts += 1;
        attempt(ctx, endpoint)
    })
    .retry(policy.delays())
    .sleep(sleep)
    .context(ctx)
    .when(Error::is_retryable)
    .await
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::VecDeque;

    type ExpectElapsed = fn(Duration);
    type ExpectAttempts = fn(usize, Result<()>);
    type ExpectEndpoints = fn(&[String], Result<()>);

    fn internal() -> Error {
        Error::Internal("boom".to_string())
    }

    fn invalid_argument() -> Error {
        Error::InvalidArgument("bad".to_string())
    }

    fn policy(max_retries: u8) -> RetryPolicy {
        RetryPolicy {
            max_retries,
            ..Default::default()
        }
    }

    #[test]
    fn delays_follow_the_backoff_within_max_retries() {
        let exponential = ExponentialBuilder::new()
            .with_min_delay(Duration::from_millis(100))
            .with_max_delay(Duration::from_secs(1));
        let test_cases = vec![
            (None, 3, vec![Duration::ZERO; 3]),
            (None, 0, vec![]),
            (
                Some(exponential),
                5,
                [100, 200, 400, 800, 1000]
                    .map(Duration::from_millis)
                    .to_vec(),
            ),
            (
                Some(exponential.with_max_times(1)),
                2,
                vec![Duration::from_millis(100), Duration::from_millis(200)],
            ),
            (Some(exponential), 0, vec![]),
        ];

        for (backoff, max_retries, expected) in test_cases {
            let policy = RetryPolicy {
                max_retries,
                backoff,
            };
            let delays: Vec<Duration> = policy.delays().collect();
            assert_eq!(delays.len(), expected.len(), "policy: {policy:?}");
            for (delay, expected) in delays.iter().zip(expected) {
                assert!(
                    delay.abs_diff(expected) < Duration::from_millis(1),
                    "policy: {policy:?}, delay: {delay:?}, expected: {expected:?}"
                );
            }
        }
    }

    #[tokio::test]
    async fn retries_wait_the_backoff_delays() {
        let delay = Duration::from_millis(20);
        let test_cases: Vec<(Option<ExponentialBuilder>, ExpectElapsed)> = vec![
            (None, |elapsed| {
                assert!(elapsed < Duration::from_millis(20), "elapsed: {elapsed:?}");
            }),
            (
                Some(
                    ExponentialBuilder::new()
                        .with_min_delay(delay)
                        .with_max_delay(delay * 2),
                ),
                |elapsed| {
                    assert!(
                        (Duration::from_millis(60)..Duration::from_millis(120)).contains(&elapsed),
                        "elapsed: {elapsed:?}"
                    );
                },
            ),
        ];

        for (backoff, expect) in test_cases {
            let policy = RetryPolicy {
                max_retries: 2,
                backoff,
            };
            let endpoints = vec!["a".to_string()];

            let start = tokio::time::Instant::now();
            let result: Result<()> = retry(policy, || async { Err(internal()) }).await;
            assert!(result.is_err(), "policy: {policy:?}");
            expect(start.elapsed());

            let start = tokio::time::Instant::now();
            let ((), result) =
                retry_with_endpoints(policy, &endpoints, (), |(), _endpoint| async {
                    ((), Err::<(), _>(internal()))
                })
                .await;
            assert!(result.is_err(), "policy: {policy:?}");
            expect(start.elapsed());
        }
    }

    #[tokio::test]
    async fn retry_stops_on_success_definitive_failure_or_exhausted_retries() {
        let test_cases: Vec<(u8, Vec<Error>, ExpectAttempts)> = vec![
            (3, vec![], |attempts, result| {
                assert!(result.is_ok(), "result: {result:?}");
                assert_eq!(attempts, 1);
            }),
            (3, vec![internal(), internal()], |attempts, result| {
                assert!(result.is_ok(), "result: {result:?}");
                assert_eq!(attempts, 3);
            }),
            (3, vec![invalid_argument()], |attempts, result| {
                assert!(matches!(result, Err(Error::InvalidArgument(_))));
                assert_eq!(attempts, 1);
            }),
            (
                2,
                vec![internal(), internal(), internal()],
                |attempts, result| {
                    assert!(matches!(result, Err(Error::Internal(_))));
                    assert_eq!(attempts, 3);
                },
            ),
            (0, vec![internal()], |attempts, result| {
                assert!(matches!(result, Err(Error::Internal(_))));
                assert_eq!(attempts, 1);
            }),
        ];

        for (max_retries, failures, expect) in test_cases {
            let mut failures = VecDeque::from(failures);
            let mut attempts = 0usize;
            let result = retry(policy(max_retries), || {
                attempts += 1;
                let failure = failures.pop_front();
                async move { failure.map_or(Ok(()), Err) }
            })
            .await;
            expect(attempts, result);
        }
    }

    #[tokio::test]
    async fn retry_with_endpoints_rotates_the_endpoints_and_stops_like_retry() {
        let test_cases: Vec<(Vec<&str>, u8, Vec<Error>, ExpectEndpoints)> = vec![
            (vec![], 3, vec![], |attempts, result| {
                assert!(matches!(result, Err(Error::InvalidArgument(_))));
                assert!(attempts.is_empty());
            }),
            (vec!["a"], 3, vec![], |attempts, result| {
                assert!(result.is_ok(), "result: {result:?}");
                assert_eq!(attempts, ["a"]);
            }),
            (
                vec!["a", "b"],
                3,
                vec![internal(), internal()],
                |attempts, result| {
                    assert!(result.is_ok(), "result: {result:?}");
                    assert_eq!(attempts.len(), 3);
                    assert_ne!(attempts[0], attempts[1]);
                    assert_eq!(attempts[0], attempts[2]);
                },
            ),
            (
                vec!["a", "b", "c"],
                3,
                vec![internal(), internal(), internal()],
                |attempts, result| {
                    assert!(result.is_ok(), "result: {result:?}");
                    assert_eq!(attempts.len(), 4);
                    let mut first: Vec<&str> = attempts[..3].iter().map(String::as_str).collect();
                    first.sort_unstable();
                    assert_eq!(first, ["a", "b", "c"]);
                    assert_eq!(attempts[0], attempts[3]);
                },
            ),
            (
                vec!["a"],
                3,
                vec![invalid_argument()],
                |attempts, result| {
                    assert!(matches!(result, Err(Error::InvalidArgument(_))));
                    assert_eq!(attempts.len(), 1);
                },
            ),
            (
                vec!["a", "b"],
                2,
                vec![internal(), internal(), internal()],
                |attempts, result| {
                    assert!(matches!(result, Err(Error::Internal(_))));
                    assert_eq!(attempts.len(), 3);
                    assert_ne!(attempts[0], attempts[1]);
                    assert_eq!(attempts[0], attempts[2]);
                },
            ),
        ];

        for (endpoints, max_retries, failures, expect) in test_cases {
            let endpoints: Vec<String> = endpoints
                .iter()
                .map(|endpoint| endpoint.to_string())
                .collect();
            let mut failures = VecDeque::from(failures);
            let (attempts, result) = retry_with_endpoints(
                policy(max_retries),
                &endpoints,
                Vec::new(),
                |mut attempts: Vec<String>, endpoint| {
                    attempts.push(endpoint);
                    let failure = failures.pop_front();
                    async move { (attempts, failure.map_or(Ok(()), Err)) }
                },
            )
            .await;
            expect(&attempts, result);
        }
    }
}
