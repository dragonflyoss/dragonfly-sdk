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

use dragonfly_api::errordetails::v2::Backend;
use reqwest;
use std::collections::HashMap;
use tonic::Code;

#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error{"request timeout: {0}"}]
    RequestTimeout(String),

    #[error{"invalid argument: {0}"}]
    InvalidArgument(String),

    #[error{"request internal error: {0}"}]
    Internal(String),

    #[error{"host {0} not found"}]
    HostNotFound(String),

    #[allow(clippy::enum_variant_names)]
    #[error(transparent)]
    BackendError(#[from] BackendError),

    #[allow(clippy::enum_variant_names)]
    #[error(transparent)]
    ProxyError(#[from] ProxyError),

    #[allow(clippy::enum_variant_names)]
    #[error(transparent)]
    DfdaemonError(#[from] DfdaemonError),

    #[allow(clippy::enum_variant_names)]
    #[error(transparent)]
    NetAddrParseError(#[from] std::net::AddrParseError),

    #[error(transparent)]
    TonicStatus(#[from] tonic::Status),

    #[allow(clippy::enum_variant_names)]
    #[error(transparent)]
    TonicTransportError(#[from] tonic::transport::Error),
}

/// Implements the error.
impl Error {
    /// Whether a failed attempt is worth another: the seed peer could not be reached
    /// or answered too slowly, the dfdaemon failed, or the proxy or backend answered
    /// `5xx`, `408` or `429`, since another seed peer may not be rate limited. Every
    /// other answer is definitive for the request, so retrying would only repeat it.
    pub(crate) fn is_retryable(&self) -> bool {
        match self {
            Error::RequestTimeout(_) | Error::Internal(_) | Error::DfdaemonError(_) => true,
            Error::ProxyError(ProxyError { status_code, .. })
            | Error::BackendError(BackendError { status_code, .. }) => {
                status_code.is_none_or(|status| {
                    status.is_server_error()
                        || status == reqwest::StatusCode::REQUEST_TIMEOUT
                        || status == reqwest::StatusCode::TOO_MANY_REQUESTS
                })
            }
            Error::TonicStatus(status) => matches!(
                status.code(),
                Code::Internal | Code::Unavailable | Code::Unknown | Code::Aborted
            ),
            Error::InvalidArgument(_)
            | Error::HostNotFound(_)
            | Error::NetAddrParseError(_)
            | Error::TonicTransportError(_) => false,
        }
    }

    /// Converts the status of a failed dfdaemon download task into the error. The
    /// dfdaemon encodes a backend failure into the status details with the backend's
    /// status code, which is kept so a definitive answer such as `404` is not retried.
    /// A deadline is a request timeout, anything else stays the status itself.
    pub(crate) fn from_status(status: tonic::Status) -> Error {
        if let Ok(backend) = serde_json::from_slice::<Backend>(status.details()) {
            return Error::BackendError(BackendError {
                message: Some(backend.message),
                header: backend.header,
                status_code: backend
                    .status_code
                    .and_then(|code| u16::try_from(code).ok())
                    .and_then(|code| reqwest::StatusCode::from_u16(code).ok()),
            });
        }

        match status.code() {
            Code::DeadlineExceeded => Error::RequestTimeout(status.message().to_string()),
            _ => Error::TonicStatus(status),
        }
    }
}

/// The error detail for Backend.
#[derive(Debug, thiserror::Error)]
#[error(
    "backend server error, message: {message:?}, header: {header:?}, status_code: {status_code:?}"
)]
pub struct BackendError {
    /// Backend error message.
    pub message: Option<String>,

    /// Backend HTTP response header.
    pub header: HashMap<String, String>,

    /// Backend HTTP status code.
    pub status_code: Option<reqwest::StatusCode>,
}

/// The error detail for Proxy.
#[derive(Debug, thiserror::Error)]
#[error(
    "proxy server error, message: {message:?}, header: {header:?}, status_code: {status_code:?}"
)]
pub struct ProxyError {
    /// Proxy error message.
    pub message: Option<String>,

    /// Proxy HTTP response header.
    pub header: HashMap<String, String>,

    /// Proxy HTTP status code.
    pub status_code: Option<reqwest::StatusCode>,
}

/// The error detail for Dfdaemon.
#[derive(Debug, thiserror::Error)]
#[error("dfdaemon error, message: {message:?}")]
pub struct DfdaemonError {
    /// Dfdaemon error message.
    pub message: Option<String>,
}

#[cfg(test)]
mod tests {
    use super::*;
    use reqwest::StatusCode;

    /// The check of the error a status converts into.
    type Expected = fn(&Error) -> bool;

    fn proxy(status_code: Option<StatusCode>) -> Error {
        Error::ProxyError(ProxyError {
            message: None,
            header: HashMap::new(),
            status_code,
        })
    }

    fn backend(status_code: Option<StatusCode>) -> Error {
        Error::BackendError(BackendError {
            message: None,
            header: HashMap::new(),
            status_code,
        })
    }

    fn status(code: Code) -> Error {
        Error::TonicStatus(tonic::Status::new(code, "status"))
    }

    #[test]
    fn is_retryable_marks_only_transient_failures() {
        let test_cases = vec![
            (Error::RequestTimeout("timeout".to_string()), true),
            (Error::Internal("boom".to_string()), true),
            (Error::DfdaemonError(DfdaemonError { message: None }), true),
            (proxy(None), true),
            (backend(None), true),
            (proxy(Some(StatusCode::INTERNAL_SERVER_ERROR)), true),
            (proxy(Some(StatusCode::BAD_GATEWAY)), true),
            (proxy(Some(StatusCode::SERVICE_UNAVAILABLE)), true),
            (proxy(Some(StatusCode::REQUEST_TIMEOUT)), true),
            (proxy(Some(StatusCode::TOO_MANY_REQUESTS)), true),
            (backend(Some(StatusCode::INTERNAL_SERVER_ERROR)), true),
            (backend(Some(StatusCode::REQUEST_TIMEOUT)), true),
            (backend(Some(StatusCode::TOO_MANY_REQUESTS)), true),
            (proxy(Some(StatusCode::BAD_REQUEST)), false),
            (proxy(Some(StatusCode::UNAUTHORIZED)), false),
            (proxy(Some(StatusCode::FORBIDDEN)), false),
            (proxy(Some(StatusCode::NOT_FOUND)), false),
            (proxy(Some(StatusCode::RANGE_NOT_SATISFIABLE)), false),
            (backend(Some(StatusCode::NOT_FOUND)), false),
            (status(Code::Internal), true),
            (status(Code::Unavailable), true),
            (status(Code::Unknown), true),
            (status(Code::Aborted), true),
            (status(Code::InvalidArgument), false),
            (status(Code::NotFound), false),
            (status(Code::PermissionDenied), false),
            (status(Code::Unauthenticated), false),
            (status(Code::ResourceExhausted), false),
            (Error::InvalidArgument("bad".to_string()), false),
            (Error::HostNotFound("host".to_string()), false),
        ];

        for (err, expected) in test_cases {
            assert_eq!(err.is_retryable(), expected, "error: {err:?}");
        }
    }

    #[test]
    fn from_status_keeps_the_backend_status_and_retryability() {
        let backend_details = serde_json::to_vec(&Backend {
            message: "origin said no".to_string(),
            header: HashMap::from([("x-served-by".to_string(), "origin".to_string())]),
            status_code: Some(404),
        })
        .unwrap();
        let test_cases: Vec<(tonic::Status, Expected, bool)> = vec![
            (
                tonic::Status::with_details(
                    Code::Internal,
                    "backend error",
                    backend_details.into(),
                ),
                |err| {
                    matches!(
                        err,
                        Error::BackendError(BackendError {
                            message: Some(message),
                            header,
                            status_code: Some(StatusCode::NOT_FOUND),
                        }) if message == "origin said no" && header["x-served-by"] == "origin"
                    )
                },
                false,
            ),
            (
                tonic::Status::deadline_exceeded("too slow"),
                |err| matches!(err, Error::RequestTimeout(_)),
                true,
            ),
            (
                tonic::Status::internal("storage is full"),
                |err| matches!(err, Error::TonicStatus(status) if status.code() == Code::Internal),
                true,
            ),
            (
                tonic::Status::not_found("no task"),
                |err| matches!(err, Error::TonicStatus(status) if status.code() == Code::NotFound),
                false,
            ),
        ];

        for (status, expected, expected_retryable) in test_cases {
            let message = status.message().to_string();
            let err = Error::from_status(status);
            assert!(expected(&err), "status: {message}, error: {err:?}");
            assert_eq!(err.is_retryable(), expected_retryable, "status: {message}");
        }
    }
}
