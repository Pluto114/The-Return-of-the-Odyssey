if(NOT ODYSSEY_CLIENT_DEPS)
    message(STATUS "Odyssey: directory scaffold only; no game executable is defined.")
    return()
endif()

find_package(raylib CONFIG REQUIRED)
find_package(asio CONFIG REQUIRED)
find_package(Protobuf CONFIG REQUIRED)
find_package(imgui CONFIG REQUIRED)
find_package(Threads REQUIRED)

add_library(odyssey_client_dependencies INTERFACE)
target_compile_features(odyssey_client_dependencies INTERFACE cxx_std_20)
target_compile_definitions(odyssey_client_dependencies INTERFACE ASIO_STANDALONE)
target_link_libraries(odyssey_client_dependencies INTERFACE
    raylib asio::asio protobuf::libprotobuf imgui::imgui Threads::Threads)

# Only generated protocol code is compiled in the scaffolding phase.
set(protocol_sources)
foreach(schema common system session lobby game stage)
    set(source "${CMAKE_CURRENT_SOURCE_DIR}/generated/protocol/${schema}.pb.cc")
    if(NOT EXISTS "${source}")
        message(FATAL_ERROR "Run pwsh -File scripts/generate-proto/generate.ps1 first.")
    endif()
    list(APPEND protocol_sources "${source}")
endforeach()
add_library(odyssey_protocol STATIC ${protocol_sources})
set_target_properties(odyssey_protocol PROPERTIES CXX_STANDARD 20 CXX_STANDARD_REQUIRED YES CXX_EXTENSIONS NO)
target_include_directories(odyssey_protocol PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}/generated/protocol")
target_link_libraries(odyssey_protocol PUBLIC protobuf::libprotobuf)

# Protocol-independent client core: frame envelope codec and stream framing.
# Kept free of raylib/asio/protobuf so it can be unit tested headlessly.
add_library(odyssey_client_core STATIC
    src/core/Frame.cpp
    src/core/FramingReader.cpp)
target_include_directories(odyssey_client_core PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}/src")
set_target_properties(odyssey_client_core PROPERTIES CXX_STANDARD 20 CXX_STANDARD_REQUIRED YES CXX_EXTENSIONS NO)
target_compile_features(odyssey_client_core PUBLIC cxx_std_20)

# Headless unit tests for the core library (no window, no sockets).
include(CTest)
if(BUILD_TESTING)
    add_executable(odyssey_core_tests tests/core_tests.cpp)
    target_include_directories(odyssey_core_tests PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}/src")
    target_link_libraries(odyssey_core_tests PRIVATE odyssey_client_core Threads::Threads)
    set_target_properties(odyssey_core_tests PROPERTIES CXX_STANDARD 20 CXX_STANDARD_REQUIRED YES CXX_EXTENSIONS NO)
    add_test(NAME odyssey_core_tests COMMAND odyssey_core_tests)
endif()

# The game client executable. Frame/network/render wiring lands incrementally
# on feature/client; protocol payload integration waits for the owner-reviewed
# message set (A) so the client never hand-writes a second protocol definition.
add_executable(odyssey_client src/main.cpp)
target_include_directories(odyssey_client PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}/src")
target_link_libraries(odyssey_client PRIVATE odyssey_client_dependencies)
set_target_properties(odyssey_client PROPERTIES CXX_STANDARD 20 CXX_STANDARD_REQUIRED YES CXX_EXTENSIONS NO)
