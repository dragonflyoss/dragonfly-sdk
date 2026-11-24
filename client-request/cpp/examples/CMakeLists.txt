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

# Builds the examples in the tree of the library, or on their own against an
# installed library.
cmake_minimum_required(VERSION 3.24)

if (NOT TARGET dragonfly::client_request)
  project(dragonfly-client-request-examples LANGUAGES CXX)
  find_package(dragonfly-client-request CONFIG REQUIRED)
endif ()

foreach (
  example
  delete
  get
  get_with_endpoints
  get_with_seed_peers
  lookup
  preheat
  replicas
  retry
  stat)
  add_executable(${example}_example ${example}.cc)
  set_target_properties(${example}_example PROPERTIES OUTPUT_NAME ${example})
  target_link_libraries(${example}_example PRIVATE dragonfly::client_request)
endforeach ()
