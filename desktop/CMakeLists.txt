cmake_minimum_required(VERSION 3.21)

project(FeedVideoDesktop VERSION 0.1.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_AUTOMOC ON)

find_package(Qt6 6.6 REQUIRED COMPONENTS Core Gui Quick QuickControls2 Network Multimedia)

qt_standard_project_setup(REQUIRES 6.6)

qt_add_executable(feedvideo
    src/main.cpp
    src/theme/theme.h
    src/theme/theme.cpp
    src/native/frameless_window.h
    src/native/frameless_window.cpp
    src/api/api_client.h
    src/api/api_client.cpp
    src/api/cookie_jar.h
    src/api/cookie_jar.cpp
    src/api/secret_box.h
    src/api/secret_box.cpp
)

target_include_directories(feedvideo PRIVATE src)

# QML and assets are globbed so adding a screen never needs a CMake edit.
# RELATIVE keeps the resource alias stable (":/qml/...", ":/assets/...").
file(GLOB_RECURSE QML_SOURCES RELATIVE "${CMAKE_CURRENT_SOURCE_DIR}" CONFIGURE_DEPENDS "qml/*.qml")
file(GLOB_RECURSE QML_JS RELATIVE "${CMAKE_CURRENT_SOURCE_DIR}" CONFIGURE_DEPENDS "qml/*.js")
file(GLOB_RECURSE ASSETS RELATIVE "${CMAKE_CURRENT_SOURCE_DIR}" CONFIGURE_DEPENDS "assets/*")

qt_add_resources(feedvideo "app"
    PREFIX "/"
    FILES ${QML_SOURCES} ${QML_JS} ${ASSETS}
)

target_link_libraries(feedvideo PRIVATE
    Qt6::Core Qt6::Gui Qt6::Quick Qt6::QuickControls2 Qt6::Network Qt6::Multimedia
)

if(WIN32)
    target_link_libraries(feedvideo PRIVATE dwmapi crypt32)
    target_compile_definitions(feedvideo PRIVATE NOMINMAX WIN32_LEAN_AND_MEAN)
endif()

install(TARGETS feedvideo
    BUNDLE DESTINATION .
    RUNTIME DESTINATION bin
)
