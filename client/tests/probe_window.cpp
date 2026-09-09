// Diagnostic probe: raylib window WITHOUT network/protocol code.
// Prints markers to stdout so a hang can be located (window init vs loop).
#include "raylib.h"

#include <cstdio>

int main() {
    std::printf("probe: before InitWindow\n");
    fflush(stdout);
    InitWindow(640, 360, "Odyssey window probe");
    std::printf("probe: after InitWindow\n");
    fflush(stdout);
    SetTargetFPS(60);

    constexpr double kFrameSeconds = 1.0 / 60.0;
    int frame = 0;
    while (true) {
        PollInputEvents();
        if (WindowShouldClose() || frame >= 1800) {
            break;
        }
        const double frame_start = GetTime();
        if ((frame % 60) == 0) {
            std::printf("probe: frame %d fps=%d\n", frame, GetFPS());
            fflush(stdout);
        }
        BeginDrawing();
        ClearBackground(RAYWHITE);
        DrawText("probe window", 24, 24, 24, DARKGRAY);
        EndDrawing();
        const double frame_elapsed = GetTime() - frame_start;
        if (frame_elapsed < kFrameSeconds) {
            WaitTime(kFrameSeconds - frame_elapsed);
        }
        ++frame;
    }
    std::printf("probe: loop exited (frame=%d)\n", frame);
    fflush(stdout);
    CloseWindow();
    std::printf("probe: after CloseWindow\n");
    fflush(stdout);
    return 0;
}
