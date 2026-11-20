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

#include "src/grpc_util.h"

#include "absl/strings/str_cat.h"
#include "src/net.h"

namespace dragonfly::client_request::internal {

std::shared_ptr<grpc::Channel> NewChannel(const std::string& address) {
  grpc::ChannelArguments arguments;
  arguments.SetMaxReceiveMessageSize(-1);
  arguments.SetMaxSendMessageSize(-1);
  return grpc::CreateCustomChannel(address, grpc::InsecureChannelCredentials(),
                                   arguments);
}

std::unique_ptr<dfdaemon::v2::DfdaemonUpload::Stub> NewDfdaemonStub(
    const SeedPeer& seed_peer) {
  return dfdaemon::v2::DfdaemonUpload::NewStub(
      NewChannel(JoinHostPort(seed_peer.ip, seed_peer.port)));
}

void SetTimeout(grpc::ClientContext* context,
                std::chrono::milliseconds timeout) {
  context->set_deadline(std::chrono::system_clock::now() + timeout);
}

absl::Status FromGrpcStatus(const grpc::Status& status,
                            absl::string_view context) {
  return absl::Status(static_cast<absl::StatusCode>(status.error_code()),
                      absl::StrCat(context, ": ", status.error_message()));
}

}  // namespace dragonfly::client_request::internal
