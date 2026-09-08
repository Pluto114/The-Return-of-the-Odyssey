if(NOT ODYSSEY_CLIENT_DEPS)
    message(STATUS "Odyssey: directory scaffold only; no game executable is defined.")
    return()
endif()

find_package(raylib CONFIG REQUIRED)
find_package(asio CONFIG REQUIRED)
find_package(Protobuf CONFIG REQUIRED)
find_package(imgui CONFIG REQUIRED)

add_library(odyssey_client_dependencies INTERFACE)
target_compile_features(odyssey_client_dependencies INTERFACE cxx_std_20)
target_compile_definitions(odyssey_client_dependencies INTERFACE ASIO_STANDALONE)
target_link_libraries(odyssey_client_dependencies INTERFACE
    raylib asio::asio protobuf::libprotobuf imgui::imgui)

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
