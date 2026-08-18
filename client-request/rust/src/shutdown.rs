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

use tokio::signal::unix::{signal, SignalKind};
use tracing::info;

/// Returns a future that will resolve when a SIGINT, SIGTERM or SIGQUIT signal is
/// received by the process.
pub async fn shutdown_signal() {
    let mut sigint = signal(SignalKind::interrupt()).unwrap();
    let mut sigterm = signal(SignalKind::terminate()).unwrap();
    let mut sigquit = signal(SignalKind::quit()).unwrap();

    tokio::select! {
        _ = sigint.recv() => {
            info!("received SIGINT, shutting down");
        },
        _ = sigterm.recv() => {
            info!("received SIGTERM, shutting down");
        }
        _ = sigquit.recv() => {
            info!("received SIGQUIT, shutting down");
        }
    }
}
