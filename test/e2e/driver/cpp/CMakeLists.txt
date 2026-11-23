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

cmake_minimum_required(VERSION 3.25)

project(
  dragonfly-client-request-e2e-driver
  DESCRIPTION "Drives the C++ client-request SDK for the e2e tests"
  LANGUAGES CXX)

set(CMAKE_CXX_EXTENSIONS OFF)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

# Builds the SDK from the tree, as the Go and the Rust drivers do.
add_subdirectory(../../../../client-request/cpp client-request
                 EXCLUDE_FROM_ALL)

find_package(absl CONFIG REQUIRED)

# Fetches CLI11 of https://github.com/CLIUtils/CLI11 for the command line and
# nlohmann/json of https://github.com/nlohmann/json for the JSON output.
include(FetchContent)
FetchContent_Declare(
  cli11
  URL "https://github.com/CLIUtils/CLI11/archive/refs/tags/v2.7.2.tar.gz"
  URL_HASH
    "SHA256=46eef3101da70852ec7af026e09d485ccee81813331c8c6052d39344443b83da"
  SYSTEM)
FetchContent_Declare(
  nlohmann_json
  URL "https://github.com/nlohmann/json/releases/download/v3.12.0/json.tar.xz"
  URL_HASH
    "SHA256=42f6e95cad6ec532fd372391373363b62a14af6d771056dbfc86160e6dfff7aa"
  SYSTEM)
FetchContent_MakeAvailable(cli11 nlohmann_json)

add_executable(driver main.cc)
target_compile_features(driver PRIVATE cxx_std_17)
target_link_libraries(
  driver PRIVATE dragonfly::client_request absl::strings CLI11::CLI11
                 nlohmann_json::nlohmann_json)
target_compile_options(
  driver PRIVATE $<$<CXX_COMPILER_ID:GNU,Clang,AppleClang>:-Wall -Wextra>)
