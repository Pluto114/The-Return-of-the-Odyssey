// odyssey_client entry point - Phase 1 D1 skeleton.
//
// Current slice (feature/client): a responsive raylib window that opens,
// renders placeholder status text, and exits cleanly on ESC / window close.
// Network Thread, frame send/receive and the connection HUD land in the next
// network slice; snapshot/render logic follows on D2. Closing the window must
// always terminate the process.
#include "raylib.h"

namespace {
constexpr int kScreenWidth = 960;
constexpr int kScreenHeight = 540;
constexpr int kFps = 60;
}  // namespace

int main() {
    InitWindow(kScreenWidth, kScreenHeight, "The Return of the Odyssey - Client");
    SetTargetFPS(kFps);

    // Placeholder until the network slice provides real connection state.
    const char* connection_line = "Connection: not started (network slice pending)";

    while (!WindowShouldClose()) {
        if (IsKeyPressed(KEY_ESCAPE)) {
            break;
        }

        BeginDrawing();
        ClearBackground(RAYWHITE);

        DrawText("The Return of the Odyssey", 24, 24, 32, DARKGRAY);
        DrawText("Phase 1 D1 skeleton - client window", 24, 64, 20, GRAY);
        DrawText(connection_line, 24, 120, 20, GRAY);
        DrawText("Press ESC or close the window to quit", 24, 160, 20, LIGHTGRAY);
        DrawFPS(kScreenWidth - 90, 12);

        EndDrawing();
    }

    CloseWindow();
    return 0;
}
