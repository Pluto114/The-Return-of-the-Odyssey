#include "input/InputSampler.h"

#include "raylib.h"

namespace odyssey::client::input {

InputSample InputSampler::SampleNow() const {
    InputSample sample;
    if (IsKeyDown(KEY_D)) {
        sample.dx = 1;
    } else if (IsKeyDown(KEY_A)) {
        sample.dx = -1;
    }
    if (IsKeyDown(KEY_S)) {
        sample.dz = 1;
    } else if (IsKeyDown(KEY_W)) {
        sample.dz = -1;
    }
    return sample;
}

}  // namespace odyssey::client::input
