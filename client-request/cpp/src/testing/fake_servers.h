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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_TESTING_FAKE_SERVERS_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_TESTING_FAKE_SERVERS_H_

#include <grpcpp/grpcpp.h>

#include <atomic>
#include <functional>
#include <memory>
#include <mutex>
#include <string>
#include <utility>
#include <vector>

#include "absl/strings/str_cat.h"
#include "dfdaemon.grpc.pb.h"
#include "dragonfly/client_request/proxy.h"
#include "grpc/health/v1/health.grpc.pb.h"
#include "scheduler.grpc.pb.h"

namespace dragonfly::client_request::internal::testing {
class FakeSeedPeer final : public dfdaemon::v2::DfdaemonUpload::Service {
 public:
  explicit FakeSeedPeer(std::string name, bool serving = true)
      : name_(std::move(name)), health_(serving) {
    grpc::ServerBuilder builder;
    builder.AddListeningPort("127.0.0.1:0", grpc::InsecureServerCredentials(),
                             &port_);
    builder.RegisterService(this);
    builder.RegisterService(&health_);
    server_ = builder.BuildAndStart();
  }

  ~FakeSeedPeer() override { server_->Shutdown(); }

  SeedPeer seed_peer(bool healthy = true) const {
    SeedPeer seed_peer;
    seed_peer.name = name_;
    seed_peer.hostname = name_;
    seed_peer.ip = "127.0.0.1";
    seed_peer.port = port_;
    seed_peer.proxy_port = port_;
    seed_peer.healthy = healthy;
    return seed_peer;
  }

  common::v2::Host host() const {
    common::v2::Host host;
    host.set_name(name_);
    host.set_hostname(name_);
    host.set_ip("127.0.0.1");
    host.set_port(port_);
    host.set_proxy_port(port_);
    return host;
  }

  dfdaemon::v2::DownloadTaskRequest download_task_request() const {
    std::scoped_lock lock(mu_);
    return download_task_requests_.back();
  }

  std::vector<dfdaemon::v2::DownloadTaskRequest> download_task_requests()
      const {
    std::scoped_lock lock(mu_);
    return download_task_requests_;
  }

  dfdaemon::v2::DeleteImageRequest delete_image_request() const {
    std::scoped_lock lock(mu_);
    return delete_image_request_;
  }

  std::function<grpc::Status()> download_task = [] { return grpc::Status::OK; };
  std::function<grpc::Status(const dfdaemon::v2::StatLocalTaskRequest&,
                             dfdaemon::v2::StatLocalTaskResponse*)>
      stat_local_task = [](const dfdaemon::v2::StatLocalTaskRequest&,
                           dfdaemon::v2::StatLocalTaskResponse*) {
        return grpc::Status(grpc::StatusCode::NOT_FOUND, "task not found");
      };
  std::function<grpc::Status()> delete_task = [] { return grpc::Status::OK; };
  std::function<grpc::Status(dfdaemon::v2::DeleteImageResponse*)> delete_image =
      [](dfdaemon::v2::DeleteImageResponse*) { return grpc::Status::OK; };

  std::atomic<int> download_task_calls{0};
  std::atomic<int> stat_local_task_calls{0};
  std::atomic<int> delete_task_calls{0};
  std::atomic<int> delete_image_calls{0};

 private:
  class Health final : public grpc::health::v1::Health::Service {
   public:
    explicit Health(bool serving) : serving_(serving) {}

    grpc::Status Check(
        grpc::ServerContext*, const grpc::health::v1::HealthCheckRequest*,
        grpc::health::v1::HealthCheckResponse* response) override {
      response->set_status(
          serving_ ? grpc::health::v1::HealthCheckResponse::SERVING
                   : grpc::health::v1::HealthCheckResponse::NOT_SERVING);
      return grpc::Status::OK;
    }

   private:
    bool serving_;
  };

  grpc::Status DownloadTask(
      grpc::ServerContext*, const dfdaemon::v2::DownloadTaskRequest* request,
      grpc::ServerWriter<dfdaemon::v2::DownloadTaskResponse>* writer) override {
    ++download_task_calls;
    {
      std::scoped_lock lock(mu_);
      download_task_requests_.push_back(*request);
    }

    grpc::Status status = download_task();
    if (status.ok()) {
      writer->Write(dfdaemon::v2::DownloadTaskResponse());
    }

    return status;
  }

  grpc::Status StatLocalTask(
      grpc::ServerContext*, const dfdaemon::v2::StatLocalTaskRequest* request,
      dfdaemon::v2::StatLocalTaskResponse* response) override {
    ++stat_local_task_calls;
    return stat_local_task(*request, response);
  }

  grpc::Status DeleteTask(grpc::ServerContext*,
                          const dfdaemon::v2::DeleteTaskRequest*,
                          google::protobuf::Empty*) override {
    ++delete_task_calls;
    return delete_task();
  }

  grpc::Status DeleteImage(
      grpc::ServerContext*, const dfdaemon::v2::DeleteImageRequest* request,
      dfdaemon::v2::DeleteImageResponse* response) override {
    ++delete_image_calls;
    {
      std::scoped_lock lock(mu_);
      delete_image_request_ = *request;
    }

    return delete_image(response);
  }

  std::string name_;
  Health health_;
  int port_ = 0;
  std::unique_ptr<grpc::Server> server_;
  mutable std::mutex mu_;
  std::vector<dfdaemon::v2::DownloadTaskRequest> download_task_requests_;
  dfdaemon::v2::DeleteImageRequest delete_image_request_;
};

class FakeScheduler final : public scheduler::v2::Scheduler::Service {
 public:
  explicit FakeScheduler(std::vector<common::v2::Host> hosts)
      : hosts_(std::move(hosts)) {
    grpc::ServerBuilder builder;
    builder.AddListeningPort("127.0.0.1:0", grpc::InsecureServerCredentials(),
                             &port_);
    builder.RegisterService(this);
    server_ = builder.BuildAndStart();
  }

  ~FakeScheduler() override { server_->Shutdown(); }

  std::string target() const { return absl::StrCat("127.0.0.1:", port_); }

  std::string endpoint() const { return absl::StrCat("http://", target()); }

  void set_hosts(std::vector<common::v2::Host> hosts) {
    std::scoped_lock lock(mu_);
    hosts_ = std::move(hosts);
  }

  scheduler::v2::ListHostsRequest list_hosts_request() const {
    std::scoped_lock lock(mu_);
    return list_hosts_request_;
  }

  std::function<grpc::Status()> list_hosts = [] { return grpc::Status::OK; };
  std::atomic<int> list_hosts_calls{0};

 private:
  grpc::Status ListHosts(grpc::ServerContext*,
                         const scheduler::v2::ListHostsRequest* request,
                         scheduler::v2::ListHostsResponse* response) override {
    ++list_hosts_calls;
    std::scoped_lock lock(mu_);
    list_hosts_request_ = *request;
    for (const auto& host : hosts_) {
      *response->add_hosts() = host;
    }

    return list_hosts();
  }

  int port_ = 0;
  std::unique_ptr<grpc::Server> server_;
  mutable std::mutex mu_;
  std::vector<common::v2::Host> hosts_;
  scheduler::v2::ListHostsRequest list_hosts_request_;
};

}  // namespace dragonfly::client_request::internal::testing

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_TESTING_FAKE_SERVERS_H_
