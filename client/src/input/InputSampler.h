// Keyboard input sampler (raylib). Turns the current key state into an
// InputSample. Lives with the window/render side of the client; the pure
// intent math and sequencing live in InputSample.h so they stay testable.
#pragma once

#include "input/InputSample.h"

namespace odyssey::client::input {

class InputSampler {
public:
    InputSample SampleNow() const;
};

}  // namespace odyssey::client::input
