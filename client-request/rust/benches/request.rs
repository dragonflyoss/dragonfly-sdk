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

//! Benchmarks for the `Request` trait implemented by `Proxy` and the
//! `RequestWithEndpoints` trait implemented by `ProxyWithEndpoints`, mirroring
//! the Go `BenchmarkGet`/`BenchmarkGetInto`/`BenchmarkGetWithEndpoints`/
//! `BenchmarkLookupEndpoints`/`BenchmarkPreheat` benchmarks: a mock scheduler
//! with a single seed peer serving a fixed body through its proxy.

use std::hint::black_box;
use std::time::Duration;

use bytes::BytesMut;
use criterion::{criterion_group, criterion_main, Criterion};
use dragonfly_api::common::v2::Host;
use dragonfly_api::dfdaemon::v2::DownloadTaskResponse;
use dragonfly_api::scheduler::v2::ListHostsResponse;
use dragonfly_client_request::{
    GetRequest, PreheatRequest, Proxy, ProxyWithEndpoints, Request, RequestWithEndpoints,
};
use futures::TryStreamExt;
use mocktail::prelude::*;
use mocktail::server::MockServer;
use tokio::runtime::Runtime;
use tonic_health::pb::health_check_response::ServingStatus;
use tonic_health::pb::HealthCheckResponse;

/// BODY_SIZE is the size of the response body served by the mock seed peer
/// proxy for the get benchmarks.
const BODY_SIZE: usize = 64 * 1024;

async fn setup_mock_scheduler(hosts: Vec<Host>) -> MockServer {
    let mut mocks = MockSet::new();
    mocks.mock(|when, then| {
        when.path("/scheduler.v2.Scheduler/ListHosts");
        then.pb(ListHostsResponse { hosts });
    });

    let server = MockServer::new_grpc("scheduler.v2.Scheduler").with_mocks(mocks);
    server.start().await.unwrap();
    server
}

async fn setup_mock_seed_peer(mut mocks: MockSet) -> MockServer {
    mocks.mock(|when, then| {
        when.path("/grpc.health.v1.Health/Check");
        then.pb(HealthCheckResponse {
            status: ServingStatus::Serving as i32,
        });
    });

    let server = MockServer::new_grpc("dfdaemon.v2.DfdaemonUpload").with_mocks(mocks);
    server.start().await.unwrap();
    server
}

fn create_seed_peer_host(name: &str, port: u16, proxy_port: u16) -> Host {
    Host {
        id: name.to_string(),
        r#type: 1,
        hostname: name.to_string(),
        ip: "127.0.0.1".to_string(),
        port: port as i32,
        proxy_port: proxy_port as i32,
        name: name.to_string(),
        ..Default::default()
    }
}

async fn setup_mock_seed_peer_proxy(mocks: MockSet) -> MockServer {
    let server = MockServer::new_http("seed-peer-proxy").with_mocks(mocks);
    server.start().await.unwrap();
    server
}

async fn setup_bench_proxy(body: Vec<u8>) -> (Proxy, String, (MockServer, MockServer, MockServer)) {
    let _ = rustls::crypto::aws_lc_rs::default_provider().install_default();
    let mut proxy_mocks = MockSet::new();
    proxy_mocks.mock(|when, then| {
        when.get().path("/file.txt");
        then.text(String::from_utf8(body).unwrap());
    });
    let mock_proxy = setup_mock_seed_peer_proxy(proxy_mocks).await;

    let mut seed_peer_mocks = MockSet::new();
    seed_peer_mocks.mock(|when, then| {
        when.path("/dfdaemon.v2.DfdaemonUpload/DownloadTask");
        then.pb_stream(vec![DownloadTaskResponse {
            host_id: "seed-peer-1".to_string(),
            task_id: "task-1".to_string(),
            peer_id: "peer-1".to_string(),
            ..Default::default()
        }]);
    });
    let mock_seed_peer = setup_mock_seed_peer(seed_peer_mocks).await;

    let mock_scheduler = setup_mock_scheduler(vec![create_seed_peer_host(
        "seed-peer-1",
        mock_seed_peer.port().unwrap(),
        mock_proxy.port().unwrap(),
    )])
    .await;

    let scheduler_endpoint = format!("http://0.0.0.0:{}", mock_scheduler.port().unwrap());
    let proxy = Proxy::builder()
        .scheduler_endpoint(scheduler_endpoint)
        .build()
        .await
        .unwrap();

    let proxy_endpoint = format!("http://127.0.0.1:{}", mock_proxy.port().unwrap());
    (
        proxy,
        proxy_endpoint,
        (mock_scheduler, mock_seed_peer, mock_proxy),
    )
}

fn bench_request(c: &mut Criterion) {
    let rt = Runtime::new().unwrap();
    let (proxy, proxy_endpoint, _servers) = rt.block_on(setup_bench_proxy(vec![b'a'; BODY_SIZE]));

    let request = GetRequest {
        url: "http://example.com/file.txt".to_string(),
        replicas: 1,
        ..Default::default()
    };

    let mut group = c.benchmark_group("request");
    group.bench_function("get", |b| {
        b.to_async(&rt).iter(|| async {
            let response = proxy.get(&request).await.unwrap();
            let mut body = response.body.unwrap();
            while let Some(chunk) = body.try_next().await.unwrap() {
                black_box(chunk);
            }
        })
    });

    group.bench_function("get_into", |b| {
        b.to_async(&rt).iter(|| async {
            let mut buf = BytesMut::new();
            proxy.get_into(&request, &mut buf).await.unwrap();
            black_box(&buf);
        })
    });

    let proxy_with_endpoints = rt.block_on(async {
        ProxyWithEndpoints::builder()
            .endpoints(vec![proxy_endpoint.clone()])
            .build()
            .await
            .unwrap()
    });
    group.bench_function("get_with_endpoints", |b| {
        b.to_async(&rt).iter(|| async {
            let response = proxy_with_endpoints.get(&request).await.unwrap();
            let mut body = response.body.unwrap();
            while let Some(chunk) = body.try_next().await.unwrap() {
                black_box(chunk);
            }
        })
    });

    let lookup_request = GetRequest {
        url: "http://example.com/file.txt".to_string(),
        ..Default::default()
    };
    group.bench_function("lookup_endpoints", |b| {
        b.to_async(&rt).iter(|| async {
            black_box(proxy.lookup_endpoints(&lookup_request).await.unwrap());
        })
    });

    let preheat_request = PreheatRequest {
        url: "http://example.com/file.txt".to_string(),
        replicas: 1,
        ..Default::default()
    };
    group
        .sample_size(30)
        .warm_up_time(Duration::from_millis(500))
        .measurement_time(Duration::from_secs(1));
    group.bench_function("preheat", |b| {
        b.to_async(&rt).iter(|| async {
            proxy.preheat(&preheat_request).await.unwrap();
        })
    });
    group.finish();
}

criterion_group!(benches, bench_request);
criterion_main!(benches);
