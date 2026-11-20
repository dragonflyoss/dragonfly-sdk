#     Copyright 2026 The Dragonfly Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Fetches the protos of the Dragonfly API and compiles them, with the gRPC
# health checking proto, into the dragonfly_client_request_protos library.

include(FetchContent)

# Keep the version in step with d7y.io/api in the Go client and dragonfly-api
# in the Rust client.
set(DRAGONFLY_API_VERSION "2.3.10")
set(DRAGONFLY_API_SHA256
    "439484e0769c46bec2874e7fb93353bf60d3707ac5e4248203abb68f156d004c")

FetchContent_Declare(
  dragonfly_api
  URL "https://github.com/dragonflyoss/api/archive/refs/tags/v${DRAGONFLY_API_VERSION}.tar.gz"
  URL_HASH "SHA256=${DRAGONFLY_API_SHA256}")
FetchContent_MakeAvailable(dragonfly_api)

if (TARGET gRPC::grpc_cpp_plugin)
  set(_grpc_cpp_plugin "$<TARGET_FILE:gRPC::grpc_cpp_plugin>")
else ()
  find_program(_grpc_cpp_plugin grpc_cpp_plugin REQUIRED)
endif ()

set(_api_proto_dir "${dragonfly_api_SOURCE_DIR}/proto")
set(_health_proto_dir "${CMAKE_CURRENT_SOURCE_DIR}/proto")
set(_generated_dir "${CMAKE_CURRENT_BINARY_DIR}/generated")
file(MAKE_DIRECTORY "${_generated_dir}")

# Compiles a proto, named without its extension, to C++ and appends the
# generated files to the sources of the library.
function (dragonfly_client_request_compile_proto proto_dir proto)
  set(_generated
      "${_generated_dir}/${proto}.pb.cc" "${_generated_dir}/${proto}.pb.h"
      "${_generated_dir}/${proto}.grpc.pb.cc"
      "${_generated_dir}/${proto}.grpc.pb.h")
  add_custom_command(
    OUTPUT ${_generated}
    COMMAND
      protobuf::protoc ARGS "--proto_path=${_api_proto_dir}"
      "--proto_path=${_health_proto_dir}" "--cpp_out=${_generated_dir}"
      "--grpc_out=${_generated_dir}"
      "--plugin=protoc-gen-grpc=${_grpc_cpp_plugin}"
      "${proto_dir}/${proto}.proto"
    DEPENDS "${proto_dir}/${proto}.proto"
    COMMENT "Running gRPC C++ protocol buffer compiler on ${proto}.proto"
    VERBATIM)
  target_sources(dragonfly_client_request_protos PRIVATE ${_generated})
endfunction ()

add_library(dragonfly_client_request_protos STATIC)
set_target_properties(
  dragonfly_client_request_protos PROPERTIES POSITION_INDEPENDENT_CODE ON
                                             CXX_CLANG_TIDY "")
target_compile_features(dragonfly_client_request_protos PUBLIC cxx_std_17)
target_include_directories(dragonfly_client_request_protos SYSTEM
                           PUBLIC "$<BUILD_INTERFACE:${_generated_dir}>")
target_link_libraries(dragonfly_client_request_protos
                      PUBLIC gRPC::grpc++ protobuf::libprotobuf)

dragonfly_client_request_compile_proto("${_api_proto_dir}" common)
dragonfly_client_request_compile_proto("${_api_proto_dir}" errordetails)
dragonfly_client_request_compile_proto("${_api_proto_dir}" scheduler)
dragonfly_client_request_compile_proto("${_api_proto_dir}" dfdaemon)
dragonfly_client_request_compile_proto("${_health_proto_dir}"
                                       grpc/health/v1/health)
