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

    /// The scripted failures of the attempts, the attempts after them succeeding.
    type Failures = Vec<fn() -> Error>;

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
        let test_cases = vec![
            (None, 2, Duration::ZERO, delay),
            (
                Some(
                    ExponentialBuilder::new()
                        .with_min_delay(delay)
                        .with_max_delay(delay * 2),
                ),
                2,
                delay * 3,
                delay * 6,
            ),
        ];

        for (backoff, max_retries, at_least, at_most) in test_cases {
            let policy = RetryPolicy {
                max_retries,
                backoff,
            };
            let endpoints = vec!["a".to_string()];

            let start = tokio::time::Instant::now();
            let result: Result<()> = retry(policy, || async { Err(internal()) }).await;
            let elapsed = start.elapsed();
            assert!(result.is_err(), "policy: {policy:?}");
            assert!(
                (at_least..at_most).contains(&elapsed),
                "policy: {policy:?}, elapsed: {elapsed:?}"
            );

            let start = tokio::time::Instant::now();
            let ((), result) =
                retry_with_endpoints(policy, &endpoints, (), |(), _endpoint| async {
                    ((), Err::<(), _>(internal()))
                })
                .await;
            let elapsed = start.elapsed();
            assert!(result.is_err(), "policy: {policy:?}");
            assert!(
                (at_least..at_most).contains(&elapsed),
                "policy: {policy:?}, elapsed: {elapsed:?}"
            );
        }
    }

    #[tokio::test]
    async fn retry_stops_on_success_definitive_failure_or_exhausted_retries() {
        let test_cases: Vec<(u8, Failures, usize, bool)> = vec![
            (3, vec![], 1, true),
            (3, vec![internal, internal], 3, true),
            (3, vec![invalid_argument], 1, false),
            (2, vec![internal, internal, internal], 3, false),
            (0, vec![internal], 1, false),
        ];

        for (max_retries, failures, expected_attempts, expected_ok) in test_cases {
            let mut failures = failures.into_iter();
            let mut attempts = 0usize;
            let result = retry(policy(max_retries), || {
                attempts += 1;
                let failure = failures.next();
                async move { failure.map_or(Ok(()), |failure| Err(failure())) }
            })
            .await;

            assert_eq!(
                result.is_ok(),
                expected_ok,
                "max_retries: {max_retries}, result: {result:?}"
            );
            assert_eq!(attempts, expected_attempts, "max_retries: {max_retries}");
        }
    }

    #[tokio::test]
    async fn retry_with_endpoints_rotates_the_endpoints_and_stops_like_retry() {
        let test_cases: Vec<(Vec<&str>, u8, Failures, usize, bool)> = vec![
            (vec![], 3, vec![], 0, false),
            (vec!["a"], 3, vec![], 1, true),
            (vec!["a", "b"], 3, vec![internal, internal], 3, true),
            (
                vec!["a", "b", "c"],
                3,
                vec![internal, internal, internal],
                4,
                true,
            ),
            (vec!["a"], 3, vec![invalid_argument], 1, false),
            (
                vec!["a", "b"],
                2,
                vec![internal, internal, internal],
                3,
                false,
            ),
        ];

        for (endpoints, max_retries, failures, expected_attempts, expected_ok) in test_cases {
            let endpoints: Vec<String> = endpoints
                .iter()
                .map(|endpoint| endpoint.to_string())
                .collect();
            let mut failures = failures.into_iter();
            let (attempts, result) = retry_with_endpoints(
                policy(max_retries),
                &endpoints,
                Vec::new(),
                |mut attempts: Vec<String>, endpoint| {
                    attempts.push(endpoint);
                    let failure = failures.next();
                    async move { (attempts, failure.map_or(Ok(()), |failure| Err(failure()))) }
                },
            )
            .await;

            assert_eq!(
                result.is_ok(),
                expected_ok,
                "endpoints: {endpoints:?}, result: {result:?}"
            );
            assert_eq!(
                attempts.len(),
                expected_attempts,
                "endpoints: {endpoints:?}"
            );

            let distinct = attempts.len().min(endpoints.len());
            let mut first: Vec<&String> = attempts.iter().take(distinct).collect();
            first.sort();
            first.dedup();
            assert_eq!(
                first.len(),
                distinct,
                "endpoints: {endpoints:?}, attempts: {attempts:?}"
            );
            for (i, attempt) in attempts.iter().enumerate().skip(endpoints.len()) {
                assert_eq!(
                    *attempt,
                    attempts[i - endpoints.len()],
                    "endpoints: {endpoints:?}, attempts: {attempts:?}"
                );
            }
        }
    }
}
