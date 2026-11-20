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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_GRPC_UTIL_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_GRPC_UTIL_H_

#include <grpcpp/grpcpp.h>

#include <chrono>
#include <memory>
#include <string>

#include "absl/status/status.h"
#include "absl/strings/string_view.h"
#include "dfdaemon.grpc.pb.h"
#include "dragonfly/client_request/proxy.h"

namespace dragonfly::client_request::internal {

/// Returns a channel to the address without transport security and without a
/// limit on the size of its messages.
std::shared_ptr<grpc::Channel> NewChannel(const std::string& address);

/// Returns the stub of the dfdaemon of a seed peer, on a connection of its
/// own.
std::unique_ptr<dfdaemon::v2::DfdaemonUpload::Stub> NewDfdaemonStub(
    const SeedPeer& seed_peer);

/// Ends the call once the timeout has passed.
void SetTimeout(grpc::ClientContext* context,
                std::chrono::milliseconds timeout);

/// Returns the failure of a call with the code of its gRPC status, so a
/// definitive one is told apart from a transient one.
absl::Status FromGrpcStatus(const grpc::Status& status,
                            absl::string_view context);

}  // namespace dragonfly::client_request::internal

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_GRPC_UTIL_H_
