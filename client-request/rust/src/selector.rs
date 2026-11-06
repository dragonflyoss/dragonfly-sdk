/*
 * Copyright 2026 The Dragonfly Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

use crate::errors::Error;
use crate::net::format_url;
use crate::shutdown;
use crate::Result;
use async_trait::async_trait;
use dashmap::DashMap;
use dragonfly_api::common::v2::Host;
use dragonfly_api::scheduler::v2::{
    scheduler_client::SchedulerClient as SchedulerGRPCClient, ListHostsRequest,
};
use dragonfly_hashring::VNodeHashRing;
use std::collections::{HashMap, HashSet};
use std::net::IpAddr;
use std::str::FromStr;
use std::time::{Duration, Instant};
use tokio::sync::{Mutex, RwLock};
use tokio::task::JoinSet;
use tonic::transport::{Channel, Endpoint};
use tonic_health::pb::{
    health_check_response::ServingStatus, health_client::HealthClient as HealthGRPCClient,
    HealthCheckRequest, HealthCheckResponse,
};
use tracing::{debug, error, info, warn, Instrument};

/// The interface for selecting item from a list of items by a specific criteria.
#[async_trait]
pub(crate) trait Selector: Send + Sync {
    /// Selects the healthy replicas of the task_id, up to `replicas` of them.
    async fn select(&self, task_id: String, replicas: u32) -> Result<Vec<Host>>;

    /// Selects every healthy item regardless of the task_id.
    async fn select_all(&self) -> Result<Vec<Host>>;
}

/// The health check timeout for seed peers.
const SEED_PEERS_HEALTH_CHECK_TIMEOUT: Duration = Duration::from_secs(3);

/// The maximum consecutive failures to connect to a seed peer proxy before it
/// is ejected.
pub(crate) const MAX_FAILED_COUNT: u32 = 5;

/// The cooldown of an ejected seed peer proxy.
const EJECTION_COOLDOWN: Duration = Duration::from_secs(30);

/// A seed peer listed by the scheduler, with the result of its health check.
#[derive(Debug, Clone, PartialEq)]
pub struct SeedPeer {
    /// The host of the seed peer.
    pub host: Host,

    /// Whether the seed peer passed its health check.
    pub healthy: bool,
}

/// The client of the scheduler for listing the seed peers to share through
/// [`ProxyWithSeedPeers`](crate::ProxyWithSeedPeers).
#[derive(Debug, Clone)]
pub struct SchedulerClient {
    /// The client to communicate with the scheduler service.
    client: SchedulerGRPCClient<Channel>,
}

/// Implements the scheduler client.
impl SchedulerClient {
    /// Creates the client connected to the scheduler at `endpoint`, bounding the
    /// connect and every request by `timeout`.
    pub async fn new(endpoint: &str, timeout: Duration) -> Result<Self> {
        let channel = Endpoint::from_shared(endpoint.to_string())
            .map_err(|err| Error::InvalidArgument(err.to_string()))?
            .connect_timeout(timeout)
            .timeout(timeout)
            .connect()
            .await
            .map_err(|err| {
                Error::Internal(format!("failed to connect to scheduler {endpoint}: {err}"))
            })?;

        Ok(Self {
            client: SchedulerGRPCClient::new(channel),
        })
    }

    /// Lists the seed peers with their health, taking up to the timeout plus 3
    /// seconds of health checks.
    pub async fn list_seed_peers(&self) -> Result<Vec<SeedPeer>> {
        // Filter for seed peer types, seed peer type is 1.
        // refer to dragonfly-client-config/src/dfdaemon.rs#HostType.
        let request = tonic::Request::new(ListHostsRequest { r#type: Some(1) });
        let response = self.client.clone().list_hosts(request).await?;
        let hosts = response.into_inner().hosts;

        // The number of seed peers in a cluster is usually small, so here we
        // directly use the number of seed peers as the concurrency for
        // simultaneous health checks.
        let mut join_set = JoinSet::new();
        for host in hosts {
            let addr = format_url("http", IpAddr::from_str(&host.ip)?, host.port as u16);
            join_set.spawn(
                async move {
                    let healthy = match Self::check_health(&addr).await {
                        Ok(response) if response.status == ServingStatus::Serving as i32 => true,
                        Ok(response) => {
                            error!("seed peer {} is not serving: {:?}", addr, response.status());
                            false
                        }
                        Err(err) => {
                            error!("health check of seed peer {} failed: {}", addr, err);
                            false
                        }
                    };

                    SeedPeer { host, healthy }
                }
                .in_current_span(),
            );
        }

        let mut seed_peers = Vec::with_capacity(join_set.len());
        while let Some(result) = join_set.join_next().await {
            match result {
                Ok(seed_peer) => seed_peers.push(seed_peer),
                Err(err) => error!("task join error: {}", err),
            }
        }

        Ok(seed_peers)
    }

    /// Checks the health of a seed peer.
    async fn check_health(addr: &str) -> Result<HealthCheckResponse> {
        // Bound the whole check, including the connect.
        tokio::time::timeout(SEED_PEERS_HEALTH_CHECK_TIMEOUT, async {
            let channel = Endpoint::from_shared(addr.to_string())?
                .connect_timeout(SEED_PEERS_HEALTH_CHECK_TIMEOUT)
                .connect()
                .await?;

            let mut client = HealthGRPCClient::new(channel);
            let mut request = tonic::Request::new(HealthCheckRequest {
                service: "".to_string(),
            });
            request.set_timeout(SEED_PEERS_HEALTH_CHECK_TIMEOUT);
            let response = client.check(request).await?;
            Ok(response.into_inner())
        })
        .await
        .map_err(|_| Error::RequestTimeout(format!("health check of {addr} timed out")))?
    }
}

/// Holds the data of seed peers.
#[derive(Debug)]
struct SeedPeers {
    /// The seed peers by host name, healthy or not.
    hosts: HashMap<String, SeedPeer>,

    /// The hashring constructed from the hosts.
    hashring: VNodeHashRing,
}

/// The ejection state of a seed peer proxy.
#[derive(Debug, Default)]
struct Ejection {
    /// The count of consecutive failures to connect.
    failed_count: u32,

    /// The time the proxy was last ejected.
    ejected_at: Option<Instant>,
}

/// The implementation of Selector for seed peers.
///
/// The hashring holds every seed peer, and a task is served only by its healthy
/// replicas.
///
/// ```text
/// task ──▶ ring position ──▶ replicas ─┬─ seed-peer-1 ──unhealthy──▶ skipped
///                                      └─ seed-peer-2 ──healthy────▶ selected
///                            every replica skipped ──▶ HostNotFound
/// ```
#[derive(Debug)]
pub(crate) struct SeedPeerSelector {
    /// The data of the seed peer selector.
    seed_peers: RwLock<SeedPeers>,

    /// The ejections of the seed peer proxies, keyed by proxy endpoint.
    ejections: DashMap<String, Ejection>,

    /// Used to protect hot refresh.
    mutex: Mutex<()>,
}

/// Implements a selector that selects seed peers.
impl SeedPeerSelector {
    /// Creates a new seed peer selector without any seed peer.
    pub(crate) fn new() -> Self {
        Self {
            seed_peers: RwLock::new(SeedPeers {
                hosts: HashMap::new(),
                hashring: VNodeHashRing::new(),
            }),
            ejections: DashMap::new(),
            mutex: Mutex::new(()),
        }
    }

    /// Starts the seed peer selector service.
    pub(crate) async fn run(
        &self,
        scheduler_client: SchedulerClient,
        health_check_interval: Duration,
    ) {
        let mut interval = tokio::time::interval(health_check_interval);
        loop {
            tokio::select! {
                _ = interval.tick() => {
                    match self.refresh(&scheduler_client).await {
                        Err(err) => error!("failed to refresh seed peers: {}", err),
                        Ok(_) => debug!("succeed to refresh seed peers"),
                    }
                }
                _ = shutdown::shutdown_signal() => {
                    info!("seed peer selector service is shutting down");
                    return;
                }
            }
        }
    }

    /// Refreshes the seed peers from the scheduler.
    pub(crate) async fn refresh(&self, scheduler_client: &SchedulerClient) -> Result<()> {
        // Only one refresh can be running at a time.
        let Ok(_guard) = self.mutex.try_lock() else {
            debug!("refresh is already running");
            return Ok(());
        };

        let seed_peers = scheduler_client.list_seed_peers().await?;
        self.update(seed_peers).await;
        Ok(())
    }

    /// Replaces the seed peers and their health.
    pub(crate) async fn update(&self, seed_peers: Vec<SeedPeer>) {
        let hosts: HashMap<String, SeedPeer> = seed_peers
            .into_iter()
            .map(|seed_peer| (seed_peer.host.name.clone(), seed_peer))
            .collect();

        // Rebuilding the ring costs vnodes-per-host hashes per host and a full
        // sort, so reuse the current ring when the host names are unchanged
        // and only refresh the host records and their health. Compare under the
        // write lock, so concurrent updates keep the hosts and the ring in step.
        {
            let mut seed_peers = self.seed_peers.write().await;
            if seed_peers.hosts.len() == hosts.len()
                && hosts.keys().all(|name| seed_peers.hosts.contains_key(name))
            {
                seed_peers.hosts = hosts;
                return;
            }
        }

        let mut hashring = VNodeHashRing::new();
        for name in hosts.keys() {
            hashring.add(name.clone());
        }

        // The write lock is held for a very short time.
        let mut seed_peers = self.seed_peers.write().await;
        seed_peers.hosts = hosts;
        seed_peers.hashring = hashring;
    }

    /// Records a failure to connect to the proxy at `endpoint`, ejecting it after
    /// [`MAX_FAILED_COUNT`] in a row.
    pub(crate) fn on_failure(&self, endpoint: &str) {
        let mut ejection = self.ejections.entry(endpoint.to_string()).or_default();
        ejection.failed_count += 1;
        if ejection.failed_count >= MAX_FAILED_COUNT {
            ejection.failed_count = 0;
            ejection.ejected_at = Some(Instant::now());

            warn!(
                "seed peer proxy {} ejected for {:?} after {} consecutive failures",
                endpoint, EJECTION_COOLDOWN, MAX_FAILED_COUNT
            );
        }
    }

    /// Records an answer of the proxy at `endpoint`, clearing its failures.
    pub(crate) fn on_success(&self, endpoint: &str) {
        // Usually there is nothing to clear, so check under the read lock first.
        if self.ejections.contains_key(endpoint) {
            self.ejections.remove(endpoint);
        }
    }

    /// Returns the endpoint of the proxy of the seed peer.
    pub(crate) fn proxy_endpoint(host: &Host) -> Result<String> {
        // TODO(chlins): Support client https scheme.
        Ok(format_url(
            "http",
            IpAddr::from_str(&host.ip).map_err(|err| Error::Internal(err.to_string()))?,
            host.proxy_port as u16,
        ))
    }

    /// Returns whether the proxy at `endpoint` is ejected.
    pub(crate) fn is_ejected(&self, endpoint: &str) -> bool {
        self.ejections
            .get(endpoint)
            .and_then(|ejection| ejection.ejected_at)
            .is_some_and(|ejected_at| ejected_at.elapsed() < EJECTION_COOLDOWN)
    }
}

#[async_trait]
impl Selector for SeedPeerSelector {
    async fn select(&self, task_id: String, replicas: u32) -> Result<Vec<Host>> {
        // Acquire a read lock and perform all logic within it.
        let seed_peers = self.seed_peers.read().await;
        if seed_peers.hosts.is_empty() {
            return Err(Error::HostNotFound("seed peers".to_string()));
        }

        // The number of replicas cannot exceed the total number of seed peers.
        let expected_replicas = std::cmp::min(replicas as usize, seed_peers.hosts.len());
        debug!("task {} expected replicas: {}", task_id, expected_replicas);

        // Walk the ring clockwise from the task position collecting distinct
        // seed peers, growing the window until enough distinct seed peers are
        // found or the whole ring is covered.
        let mut replica_seed_peers: Vec<&SeedPeer> = Vec::with_capacity(expected_replicas);
        let mut window = expected_replicas * 2;
        loop {
            replica_seed_peers.clear();
            let vnodes = seed_peers
                .hashring
                .get_with_replicas(&task_id, window)
                .unwrap_or_default();
            let vnodes_len = vnodes.len();
            let mut seen: HashSet<&str> = HashSet::with_capacity(expected_replicas);
            for vnode in vnodes.iter() {
                if !seen.insert(vnode.name()) {
                    continue;
                }

                let Some(seed_peer) = seed_peers.hosts.get(vnode.name()) else {
                    continue;
                };

                replica_seed_peers.push(seed_peer);
                if replica_seed_peers.len() == expected_replicas {
                    break;
                }
            }

            if replica_seed_peers.len() == expected_replicas
                || vnodes_len >= seed_peers.hashring.len()
            {
                break;
            }

            window *= 2;
        }

        // Select the healthy replicas, failing when none is.
        let selected: Vec<Host> = replica_seed_peers
            .into_iter()
            .filter(|seed_peer| seed_peer.healthy)
            .map(|seed_peer| seed_peer.host.clone())
            .collect();
        if selected.is_empty() {
            return Err(Error::HostNotFound("selected seed peers".to_string()));
        }

        Ok(selected)
    }

    async fn select_all(&self) -> Result<Vec<Host>> {
        // Select every seed peer that passed its health check.
        let seed_peers = self.seed_peers.read().await;
        let selected: Vec<Host> = seed_peers
            .hosts
            .values()
            .filter(|seed_peer| seed_peer.healthy)
            .map(|seed_peer| seed_peer.host.clone())
            .collect();
        if selected.is_empty() {
            return Err(Error::HostNotFound("seed peers".to_string()));
        }

        Ok(selected)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Arc;

    fn create_test_host(id: &str, ip: &str, port: i32, host_type: u32) -> Host {
        Host {
            id: id.to_string(),
            ip: ip.to_string(),
            port,
            proxy_port: port,
            r#type: host_type,
            hostname: format!("host-{id}"),
            name: format!("host-{id}"),
            ..Default::default()
        }
    }

    async fn create_test_selector() -> SeedPeerSelector {
        let selector = SeedPeerSelector::new();
        selector.seed_peers.write().await.hashring = VNodeHashRing::with_vnodes_per_node(1);
        selector
    }

    fn create_test_seed_peers(count: usize, unhealthy_id: Option<&str>) -> Vec<SeedPeer> {
        (1..=count)
            .map(|i| {
                let host = create_test_host(&i.to_string(), &format!("192.168.1.{i}"), 8080, 1);
                SeedPeer {
                    healthy: Some(host.id.as_str()) != unhealthy_id,
                    host,
                }
            })
            .collect()
    }

    async fn select_ids(selector: &SeedPeerSelector, task_id: &str, replicas: u32) -> Vec<String> {
        selector
            .select(task_id.to_string(), replicas)
            .await
            .unwrap()
            .into_iter()
            .map(|host| host.id)
            .collect()
    }

    async fn add_test_host(selector: &SeedPeerSelector, host: Host) {
        let mut seed_peers = selector.seed_peers.write().await;
        seed_peers.hashring.add(host.name.clone());
        seed_peers.hosts.insert(
            host.name.clone(),
            SeedPeer {
                host,
                healthy: true,
            },
        );
    }

    #[tokio::test]
    async fn select_with_no_hosts() {
        let selector = create_test_selector().await;

        let result = selector.select("test-task".to_string(), 2).await;
        assert!(result.is_err());
        assert!(matches!(result, Err(Error::HostNotFound(_))));
    }

    #[tokio::test]
    async fn select_with_single_host() {
        let selector = create_test_selector().await;

        let host = create_test_host("1", "192.168.1.1", 8080, 1);
        add_test_host(&selector, host).await;

        let result = selector.select("test-task".to_string(), 1).await;
        assert!(result.is_ok());

        let hosts = result.unwrap();
        assert_eq!(hosts.len(), 1);
        assert_eq!(hosts[0].id, "1");
        assert_eq!(hosts[0].ip, "192.168.1.1");
    }

    #[tokio::test]
    async fn select_replicas() {
        let test_cases = vec![(5, 3, 3), (2, 5, 2), (20, 10, 10)];

        for (host_count, replicas, expected_len) in test_cases {
            let selector = create_test_selector().await;
            for i in 1..=host_count {
                let host = create_test_host(&i.to_string(), &format!("192.168.1.{i}"), 8080, 1);
                add_test_host(&selector, host).await;
            }

            let hosts = selector
                .select("test-task".to_string(), replicas)
                .await
                .unwrap();
            assert_eq!(
                hosts.len(),
                expected_len,
                "host_count: {host_count}, replicas: {replicas}"
            );

            let seen: std::collections::HashSet<_> = hosts.iter().map(|host| &host.id).collect();
            assert_eq!(
                seen.len(),
                expected_len,
                "host_count: {host_count}, replicas: {replicas}"
            );
        }
    }

    #[tokio::test]
    async fn select_all() {
        let selector = create_test_selector().await;

        let result = selector.select_all().await;
        assert!(matches!(result, Err(Error::HostNotFound(_))));

        for i in 1..=5 {
            let host = create_test_host(&i.to_string(), &format!("192.168.1.{i}"), 8080, 1);
            add_test_host(&selector, host).await;
        }

        let hosts = selector.select_all().await.unwrap();
        assert_eq!(hosts.len(), 5);

        let seen: std::collections::HashSet<_> = hosts.iter().map(|host| &host.id).collect();
        assert_eq!(seen.len(), 5);
    }

    #[tokio::test]
    async fn select_all_skips_unhealthy_seed_peers() {
        let selector = SeedPeerSelector::new();
        selector.update(create_test_seed_peers(2, Some("2"))).await;

        let hosts = selector.select_all().await.unwrap();
        assert_eq!(hosts.len(), 1);
        assert_eq!(hosts[0].id, "1");

        selector.update(create_test_seed_peers(1, Some("1"))).await;
        assert!(matches!(
            selector.select_all().await,
            Err(Error::HostNotFound(_))
        ));
    }

    #[tokio::test]
    async fn select_consistency() {
        let selector = create_test_selector().await;

        for i in 1..=5 {
            let host = create_test_host(&i.to_string(), &format!("192.168.1.{i}"), 8080, 1);
            add_test_host(&selector, host).await;
        }

        let task_id = "consistent-task".to_string();

        let result1 = selector.select(task_id.clone(), 3).await.unwrap();
        let result2 = selector.select(task_id.clone(), 3).await.unwrap();
        let result3 = selector.select(task_id, 3).await.unwrap();

        assert_eq!(result1.len(), result2.len());
        assert_eq!(result2.len(), result3.len());

        let ids1: Vec<_> = result1.iter().map(|h| &h.id).collect();
        let ids2: Vec<_> = result2.iter().map(|h| &h.id).collect();
        let ids3: Vec<_> = result3.iter().map(|h| &h.id).collect();

        assert_eq!(ids1, ids2);
        assert_eq!(ids2, ids3);
    }

    #[tokio::test]
    async fn concurrent_select() {
        let selector = Arc::new(create_test_selector().await);

        for i in 1..=5 {
            let host = create_test_host(&i.to_string(), &format!("192.168.1.{i}"), 8080, 1);
            add_test_host(&selector, host).await;
        }

        let mut handles = vec![];
        for i in 0..10 {
            let selector = Arc::clone(&selector);
            let handle = tokio::spawn(async move {
                let task_id = format!("concurrent-task-{i}");
                selector.select(task_id, 2).await
            });
            handles.push(handle);
        }

        for handle in handles {
            let result = handle.await.unwrap();
            assert!(result.is_ok());

            let hosts = result.unwrap();
            assert_eq!(hosts.len(), 2);
        }
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    async fn concurrent_update() {
        for _ in 0..100 {
            let selector = Arc::new(SeedPeerSelector::new());
            selector.update(create_test_seed_peers(2, None)).await;

            let health_updates = tokio::spawn({
                let selector = selector.clone();
                async move {
                    for i in 0..1000 {
                        selector
                            .update(create_test_seed_peers(2, Some(&(i % 2 + 1).to_string())))
                            .await;
                    }
                }
            });
            let membership_update = tokio::spawn({
                let selector = selector.clone();
                async move { selector.update(create_test_seed_peers(3, None)).await }
            });
            health_updates.await.unwrap();
            membership_update.await.unwrap();

            let seed_peers = selector.seed_peers.read().await;
            let host_names: HashSet<String> = seed_peers.hosts.keys().cloned().collect();
            let ring_names: HashSet<String> = seed_peers
                .hashring
                .get_with_replicas("test-task", seed_peers.hashring.len())
                .unwrap()
                .iter()
                .map(|vnode| vnode.name().to_string())
                .collect();
            assert_eq!(host_names, ring_names);
        }
    }

    #[tokio::test]
    async fn select_serves_a_task_only_from_its_healthy_replicas() {
        let selector = SeedPeerSelector::new();
        selector.update(create_test_seed_peers(5, None)).await;
        let task_ids: Vec<String> = (0..64).map(|i| format!("task-{i}")).collect();
        let mut healthy_selections = Vec::with_capacity(task_ids.len());
        for task_id in task_ids.iter() {
            healthy_selections.push(select_ids(&selector, task_id, 2).await);
        }

        selector.update(create_test_seed_peers(5, Some("3"))).await;
        for (task_id, healthy_selection) in task_ids.iter().zip(healthy_selections.iter()) {
            let expected: Vec<String> = healthy_selection
                .iter()
                .filter(|id| *id != "3")
                .cloned()
                .collect();
            assert_eq!(
                select_ids(&selector, task_id, 2).await,
                expected,
                "task_id: {task_id}"
            );
        }

        selector.update(create_test_seed_peers(5, None)).await;
        for (task_id, healthy_selection) in task_ids.iter().zip(healthy_selections.iter()) {
            assert_eq!(
                &select_ids(&selector, task_id, 2).await,
                healthy_selection,
                "task_id: {task_id}"
            );
        }
    }

    #[tokio::test]
    async fn select_fails_when_every_replica_is_unhealthy() {
        let selector = SeedPeerSelector::new();
        selector
            .update(vec![
                SeedPeer {
                    host: create_test_host("1", "192.168.1.1", 8080, 1),
                    healthy: false,
                },
                SeedPeer {
                    host: create_test_host("2", "192.168.1.2", 8080, 1),
                    healthy: false,
                },
                SeedPeer {
                    host: create_test_host("3", "192.168.1.3", 8080, 1),
                    healthy: true,
                },
            ])
            .await;
        let task_id = (0..64)
            .map(|i| format!("task-{i}"))
            .find(|task_id| {
                let seed_peers = selector.seed_peers.try_read().unwrap();
                let vnodes = seed_peers.hashring.get_with_replicas(task_id, 1).unwrap();
                vnodes[0].name() != "host-3"
            })
            .unwrap();

        let result = selector.select(task_id, 1).await;
        assert!(matches!(result, Err(Error::HostNotFound(_))));
    }

    #[tokio::test]
    async fn on_failure_ejects_a_proxy_after_consecutive_failures_until_a_success() {
        let selector = SeedPeerSelector::new();
        selector.update(create_test_seed_peers(1, None)).await;
        let replica = selector.select("test-task".to_string(), 1).await.unwrap()[0].clone();
        let endpoint = SeedPeerSelector::proxy_endpoint(&replica).unwrap();

        for _ in 1..MAX_FAILED_COUNT {
            selector.on_failure(&endpoint);
        }
        assert!(!selector.is_ejected(&endpoint));

        selector.on_failure(&endpoint);
        assert!(selector.is_ejected(&endpoint));
        assert_eq!(
            select_ids(&selector, "test-task", 1).await,
            [replica.id.clone()]
        );

        selector.on_success(&endpoint);
        assert!(!selector.is_ejected(&endpoint));
    }
}
