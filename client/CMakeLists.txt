if(NOT ODYSSEY_CLIENT_DEPS)
    message(STATUS "Odyssey: directory scaffold only; no game executable is defined.")
    return()
endif()

find_package(raylib CONFIG REQUIRED)
find_package(asio CONFIG REQUIRED)
find_package(Protobuf CONFIG REQUIRED)
find_package(imgui CONFIG REQUIRED)
find_package(Threads REQUIRED)
# raylib's static build does not propagate its glfw dependency; link it
# explicitly (harmless for the dynamic case where raylib.dll loads glfw3.dll).
find_package(glfw3 CONFIG REQUIRED)

# data/equipment/catalog.json is the only hand-maintained equipment display
# source. Generate a small runtime TSV directly from its version/id/slot/name/
# description fields so the C++ client cannot drift to a second manual table.
set(_equipment_catalog "${PROJECT_SOURCE_DIR}/data/equipment/catalog.json")
if(NOT EXISTS "${_equipment_catalog}")
    message(FATAL_ERROR "Missing equipment catalog: ${_equipment_catalog}")
endif()
file(READ "${_equipment_catalog}" _equipment_json)
string(JSON _equipment_version GET "${_equipment_json}" version)
if(NOT _equipment_version EQUAL 1)
    message(FATAL_ERROR "Equipment catalog version ${_equipment_version}; client expects version 1")
endif()
string(JSON _equipment_count LENGTH "${_equipment_json}" items)
if(_equipment_count LESS 1)
    message(FATAL_ERROR "Equipment catalog contains no display entries")
endif()
set(_equipment_display "${CMAKE_CURRENT_BINARY_DIR}/equipment.tsv")
file(WRITE "${_equipment_display}" "# Generated from data/equipment/catalog.json; do not edit.\n# catalog-version=${_equipment_version}\n")
set(_equipment_localized_catalog "${CMAKE_CURRENT_SOURCE_DIR}/assets/equipment.zh-CN.json")
file(READ "${_equipment_localized_catalog}" _equipment_localized_json)
string(JSON _equipment_localized_version GET "${_equipment_localized_json}" version)
string(JSON _equipment_localized_count LENGTH "${_equipment_localized_json}" items)
if(NOT _equipment_localized_version EQUAL _equipment_version OR
   NOT _equipment_localized_count EQUAL _equipment_count)
    message(FATAL_ERROR "Chinese equipment names must match the authoritative catalog version and item count")
endif()
set(_equipment_localized_display "${CMAKE_CURRENT_BINARY_DIR}/equipment.zh-CN.tsv")
file(WRITE "${_equipment_localized_display}" "# Chinese player-facing labels keyed by authoritative equipment ID.\n# catalog-version=${_equipment_version}\n")
math(EXPR _equipment_last "${_equipment_count} - 1")
foreach(_equipment_index RANGE 0 ${_equipment_last})
    string(JSON _equipment_id GET "${_equipment_json}" items ${_equipment_index} id)
    string(JSON _equipment_name GET "${_equipment_json}" items ${_equipment_index} name)
    string(JSON _equipment_slot GET "${_equipment_json}" items ${_equipment_index} slot)
    string(JSON _equipment_description GET "${_equipment_json}" items ${_equipment_index} description)
    foreach(_equipment_value _equipment_name _equipment_slot _equipment_description)
        if("${${_equipment_value}}" MATCHES "[\t\r\n]")
            message(FATAL_ERROR "Equipment display fields cannot contain tabs or newlines")
        endif()
    endforeach()
    file(APPEND "${_equipment_display}" "${_equipment_id}\t${_equipment_name}\t${_equipment_slot}\t${_equipment_description}\n")
    foreach(_equipment_key name slot description)
        string(JSON _localized_${_equipment_key} ERROR_VARIABLE _localized_error
            GET "${_equipment_localized_json}" items "${_equipment_id}" ${_equipment_key})
        if(_localized_error OR "${_localized_${_equipment_key}}" MATCHES "[\t\r\n]")
            message(FATAL_ERROR "Missing or invalid Chinese ${_equipment_key} for equipment ${_equipment_id}")
        endif()
    endforeach()
    file(APPEND "${_equipment_localized_display}"
        "${_equipment_id}\t${_localized_name}\t${_localized_slot}\t${_localized_description}\n")
endforeach()

add_library(odyssey_client_dependencies INTERFACE)
target_compile_features(odyssey_client_dependencies INTERFACE cxx_std_20)
target_compile_definitions(odyssey_client_dependencies INTERFACE ASIO_STANDALONE)
target_link_libraries(odyssey_client_dependencies INTERFACE
    raylib glfw asio::asio protobuf::libprotobuf imgui::imgui Threads::Threads)

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

# Network Thread slice: standalone Asio TCP client. Links the frame codec from
# core; payload decoding stays out of this layer until A's protocol lands.
add_library(odyssey_net_deps INTERFACE)
target_compile_features(odyssey_net_deps INTERFACE cxx_std_20)
target_compile_definitions(odyssey_net_deps INTERFACE ASIO_STANDALONE)
if(WIN32)
    target_compile_definitions(odyssey_net_deps INTERFACE _WIN32_WINNT=0x0601)
    target_link_libraries(odyssey_net_deps INTERFACE ws2_32)
endif()
target_link_libraries(odyssey_net_deps INTERFACE asio::asio Threads::Threads)

add_library(odyssey_client_net STATIC src/network/NetClient.cpp)
target_include_directories(odyssey_client_net PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}/src")
set_target_properties(odyssey_client_net PROPERTIES CXX_STANDARD 20 CXX_STANDARD_REQUIRED YES CXX_EXTENSIONS NO)
target_link_libraries(odyssey_client_net PUBLIC odyssey_client_core odyssey_net_deps)

# Headless unit tests for the core library (no window, no sockets).
# BUILD_TESTING comes from include(CTest) in the root CMakeLists.
if(BUILD_TESTING)
    add_executable(odyssey_core_tests tests/core_tests.cpp)
    target_include_directories(odyssey_core_tests PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}/src")
    target_link_libraries(odyssey_core_tests PRIVATE odyssey_client_core Threads::Threads)
    set_target_properties(odyssey_core_tests PROPERTIES CXX_STANDARD 20 CXX_STANDARD_REQUIRED YES CXX_EXTENSIONS NO)
    add_test(NAME odyssey_core_tests COMMAND odyssey_core_tests)

    add_executable(odyssey_net_tests tests/network_tests.cpp)
    target_include_directories(odyssey_net_tests PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}/src")
    target_link_libraries(odyssey_net_tests PRIVATE odyssey_client_net odyssey_net_deps)
    set_target_properties(odyssey_net_tests PROPERTIES CXX_STANDARD 20 CXX_STANDARD_REQUIRED YES CXX_EXTENSIONS NO)
    add_test(NAME odyssey_net_tests COMMAND odyssey_net_tests)

    add_executable(odyssey_logic_tests tests/gameview_tests.cpp)
    target_include_directories(odyssey_logic_tests PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}/src")
    target_compile_definitions(odyssey_logic_tests PRIVATE
        ODYSSEY_EQUIPMENT_ZH_PATH="${_equipment_localized_display}")
    if(MSVC)
        target_compile_options(odyssey_logic_tests PRIVATE /utf-8)
    endif()
    target_link_libraries(odyssey_logic_tests PRIVATE Threads::Threads)
    set_target_properties(odyssey_logic_tests PROPERTIES CXX_STANDARD 20 CXX_STANDARD_REQUIRED YES CXX_EXTENSIONS NO)
    add_test(NAME odyssey_logic_tests COMMAND odyssey_logic_tests)

    add_executable(odyssey_protocol_tests tests/protocol_tests.cpp)
    target_include_directories(odyssey_protocol_tests PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}/src")
    target_link_libraries(odyssey_protocol_tests PRIVATE odyssey_protocol)
    set_target_properties(odyssey_protocol_tests PROPERTIES CXX_STANDARD 20 CXX_STANDARD_REQUIRED YES CXX_EXTENSIONS NO)
    add_test(NAME odyssey_protocol_tests COMMAND odyssey_protocol_tests)
endif()

# The game client executable. Ping/Login now use A's generated protocol;
# PlayerInput transmission and WorldSnapshot decoding land in the next slices.
add_executable(odyssey_client src/main.cpp src/input/InputSampler.cpp)
target_include_directories(odyssey_client PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}/src")
if(MSVC)
    # Chinese UTF-8 literals must not be parsed as the machine's ANSI code page.
    target_compile_options(odyssey_client PRIVATE /utf-8)
endif()
target_link_libraries(odyssey_client PRIVATE
    odyssey_client_dependencies odyssey_client_net odyssey_client_core odyssey_protocol)
set_target_properties(odyssey_client PROPERTIES CXX_STANDARD 20 CXX_STANDARD_REQUIRED YES CXX_EXTENSIONS NO)
option(ODYSSEY_CLIENT_CONSOLE "Show the developer console beside the Windows game client" OFF)
if(WIN32 AND NOT ODYSSEY_CLIENT_CONSOLE)
    # Player builds launch as a normal Windows app. Keep event diagnostics in
    # log files and the in-game F3 overlay instead of exposing a console full
    # of protocol details. /ENTRY lets the existing portable main() remain the
    # single entry point on Windows.
    set_target_properties(odyssey_client PROPERTIES WIN32_EXECUTABLE YES)
    if(MSVC)
        target_link_options(odyssey_client PRIVATE /ENTRY:mainCRTStartup)
    endif()
    target_compile_definitions(odyssey_client PRIVATE ODYSSEY_PLAYER_CLIENT=1)
endif()
add_custom_command(TARGET odyssey_client POST_BUILD
    COMMAND "${CMAKE_COMMAND}" -E copy_if_different
        "${_equipment_display}" "$<TARGET_FILE_DIR:odyssey_client>/equipment.tsv"
    COMMAND "${CMAKE_COMMAND}" -E copy_if_different
        "${_equipment_localized_display}" "$<TARGET_FILE_DIR:odyssey_client>/equipment.zh-CN.tsv"
    COMMENT "Copying generated equipment display table"
    VERBATIM)

# Diagnostic: raylib window with no network/protocol (isolates environment
# issues from client-logic hangs). Prints markers to stdout.
add_executable(odyssey_window_probe tests/probe_window.cpp)
target_link_libraries(odyssey_window_probe PRIVATE odyssey_client_dependencies)
set_target_properties(odyssey_window_probe PROPERTIES CXX_STANDARD 20 CXX_STANDARD_REQUIRED YES CXX_EXTENSIONS NO)
