cmake_minimum_required(VERSION 3.31)
project(The_Return_of_the_Odyssey VERSION 0.1.0 LANGUAGES NONE)

# The scaffold configures without a compiler or external libraries.
option(ODYSSEY_CLIENT_DEPS "Resolve and validate the C++ client dependencies" OFF)
if(ODYSSEY_CLIENT_DEPS)
    enable_language(C)
    enable_language(CXX)
endif()
add_subdirectory(client)
