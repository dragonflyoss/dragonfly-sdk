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

# Fetches ada, the WHATWG url parser of https://github.com/ada-url/ada, and
# compiles it into the dragonfly_client_request_ada objects.

include(FetchContent)

# The parser decides the canonical url of a task and so its id. Keep the
# version pinned, and in step with the url crate of the Rust client.
set(DRAGONFLY_ADA_VERSION "2.9.2")
set(DRAGONFLY_ADA_SHA256
    "b2cce630590b490d79ea4f4460ba77efd5fb29c5a87a4e8cb7ebc4859bc4b564")

FetchContent_Declare(
  dragonfly_ada
  URL "https://github.com/ada-url/ada/releases/download/v${DRAGONFLY_ADA_VERSION}/singleheader.zip"
  URL_HASH "SHA256=${DRAGONFLY_ADA_SHA256}")
FetchContent_MakeAvailable(dragonfly_ada)

add_library(dragonfly_client_request_ada OBJECT
            "${dragonfly_ada_SOURCE_DIR}/ada.cpp")
set_target_properties(
  dragonfly_client_request_ada
  PROPERTIES POSITION_INDEPENDENT_CODE ON
             CXX_CLANG_TIDY ""
             COMPILE_WARNING_AS_ERROR OFF)
target_compile_features(dragonfly_client_request_ada PUBLIC cxx_std_17)
target_include_directories(dragonfly_client_request_ada SYSTEM
                           PUBLIC "${dragonfly_ada_SOURCE_DIR}")
