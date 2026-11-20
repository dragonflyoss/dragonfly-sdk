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

cmake_minimum_required(VERSION 3.24)

project(
  dragonfly-client-request
  VERSION 1.15.0
  DESCRIPTION "Dragonfly client request SDK for C++"
  HOMEPAGE_URL "https://github.com/dragonflyoss/dragonfly-sdk"
  LANGUAGES CXX)

set(CMAKE_CXX_EXTENSIONS OFF)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

include(CTest)
option(DRAGONFLY_CLIENT_REQUEST_BUILD_EXAMPLES "Build the examples."
       ${PROJECT_IS_TOP_LEVEL})

find_package(Protobuf CONFIG QUIET)
if (NOT Protobuf_FOUND)
  find_package(Protobuf REQUIRED)
endif ()
find_package(gRPC CONFIG REQUIRED)
if (NOT TARGET re2::re2)
  find_package(re2 CONFIG REQUIRED)
endif ()
find_package(absl CONFIG REQUIRED)
find_package(CURL REQUIRED)
find_package(OpenSSL REQUIRED)

include(cmake/Ada.cmake)
include(cmake/DragonflyApi.cmake)

add_library(
  dragonfly_client_request
  src/delete.cc
  src/digest.cc
  src/errors.cc
  src/get.cc
  src/grpc_util.cc
  src/hashring.cc
  src/http_client.cc
  src/http_client_pool.cc
  src/id_generator.cc
  src/net.cc
  src/oci.cc
  src/parallel.cc
  src/preheat.cc
  src/proxy.cc
  src/proxy_with_endpoints.cc
  src/range.cc
  src/request.cc
  src/retry.cc
  src/scheduler.cc
  src/selector.cc
  src/stat.cc
  src/url.cc
  $<TARGET_OBJECTS:dragonfly_client_request_ada>)
add_library(dragonfly::client_request ALIAS dragonfly_client_request)
target_compile_features(dragonfly_client_request PUBLIC cxx_std_17)
target_include_directories(
  dragonfly_client_request
  PUBLIC "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>"
         "$<INSTALL_INTERFACE:include>"
  PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}")
target_include_directories(dragonfly_client_request SYSTEM
                           PRIVATE "${dragonfly_ada_SOURCE_DIR}")
target_link_libraries(
  dragonfly_client_request
  PUBLIC absl::status absl::statusor
  PRIVATE dragonfly_client_request_protos absl::memory absl::strings
          absl::time CURL::libcurl OpenSSL::Crypto re2::re2)
target_compile_options(
  dragonfly_client_request
  PRIVATE $<$<CXX_COMPILER_ID:GNU,Clang,AppleClang>:-Wall -Wextra>)
set_target_properties(
  dragonfly_client_request
  PROPERTIES EXPORT_NAME client_request
             VERSION ${PROJECT_VERSION}
             SOVERSION ${PROJECT_VERSION_MAJOR})
set_target_properties(dragonfly_client_request_protos
                      PROPERTIES EXPORT_NAME client_request_protos)

if (DRAGONFLY_CLIENT_REQUEST_BUILD_EXAMPLES)
  add_subdirectory(examples)
endif ()

# Install the library with a package configuration, so a project finds it with
# find_package(dragonfly-client-request CONFIG).
include(GNUInstallDirs)
include(CMakePackageConfigHelpers)
set(_config_dir "${CMAKE_INSTALL_LIBDIR}/cmake/dragonfly-client-request")
install(
  TARGETS dragonfly_client_request dragonfly_client_request_protos
  EXPORT dragonfly-client-request-targets
  RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR}
  LIBRARY DESTINATION ${CMAKE_INSTALL_LIBDIR}
  ARCHIVE DESTINATION ${CMAKE_INSTALL_LIBDIR})
install(DIRECTORY include/ DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})
install(
  EXPORT dragonfly-client-request-targets
  NAMESPACE dragonfly::
  DESTINATION "${_config_dir}")
configure_package_config_file(
  cmake/dragonfly-client-request-config.cmake.in
  "${CMAKE_CURRENT_BINARY_DIR}/dragonfly-client-request-config.cmake"
  INSTALL_DESTINATION "${_config_dir}")
write_basic_package_version_file(
  "${CMAKE_CURRENT_BINARY_DIR}/dragonfly-client-request-config-version.cmake"
  VERSION ${PROJECT_VERSION}
  COMPATIBILITY SameMajorVersion)
install(
  FILES
    "${CMAKE_CURRENT_BINARY_DIR}/dragonfly-client-request-config.cmake"
    "${CMAKE_CURRENT_BINARY_DIR}/dragonfly-client-request-config-version.cmake"
  DESTINATION "${_config_dir}")

if (BUILD_TESTING AND PROJECT_IS_TOP_LEVEL)
  include(FetchContent)
  FetchContent_Declare(
    googletest
    URL "https://github.com/google/googletest/archive/refs/tags/v1.17.0.tar.gz"
    URL_HASH
      "SHA256=65fab701d9829d38cb77c14acdc431d2108bfdbf8979e40eb8ae567edf10b27c"
    FIND_PACKAGE_ARGS NAMES GTest)
  set(INSTALL_GTEST
      OFF
      CACHE BOOL "" FORCE)
  FetchContent_MakeAvailable(googletest)

  # Adds the test of a source file, linked with the library and its private
  # dependencies so the internal headers are testable.
  function (dragonfly_client_request_add_test name)
    add_executable(${name} "src/${name}.cc")
    target_include_directories(${name}
                               PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}")
    target_link_libraries(
      ${name}
      PRIVATE dragonfly_client_request dragonfly_client_request_protos
              absl::strings GTest::gmock_main)
    add_test(NAME ${name} COMMAND ${name})
  endfunction ()

  set(dragonfly_client_request_tests
      delete_test
      digest_test
      errors_test
      get_test
      hashring_test
      http_client_pool_test
      http_client_test
      id_generator_test
      oci_test
      parallel_test
      preheat_test
      proxy_test
      proxy_with_endpoints_test
      range_test
      request_test
      retry_test
      scheduler_test
      selector_test
      stat_test
      url_test)
  foreach (test IN LISTS dragonfly_client_request_tests)
    dragonfly_client_request_add_test(${test})
  endforeach ()
endif ()
