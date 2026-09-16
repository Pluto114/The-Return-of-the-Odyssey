// Per-frame timing capture for the Release UI acceptance run (P3).
//
// The acceptance criterion is the whole-frame CPU time difference between the UI on
// and the UI off in the SAME scene, averaged over at least 600 frames, so the client
// has to be able to record per-frame CPU times and stop by itself. That is what this
// does, and it is deliberately the only piece of the client that writes a file while
// running:
//
//   * fixed capacity - no allocation anywhere near the render loop,
//   * one float per frame, dumped ONCE at the end, so per-frame I/O cannot perturb
//     the very measurement it is taking,
//   * an explicit warm-up that is discarded before counting: the first sampled frame
//     carries the font atlas upload and the first shader/batch setup, which the plan
//     says must not be counted, and the next few carry the first combat-frame batch
//     setup. Both runs discard the same frames, so the comparison is unaffected.
//
// The capture is armed and then started separately, because the criterion is a BATTLE
// scene: counting from process start would spend the budget on login/matchmaking.
// `Arm` fixes the target, `BeginSampling` starts counting (either immediately, or on
// stage entry when the caller triggers on the gameplay gate).
//
// Pure and raylib-free so the statistics can be unit tested.
#pragma once

#include <algorithm>
#include <cstddef>
#include <cstdint>
#include <cstdio>
#include <string>
#include <vector>

namespace odyssey::client::ui {

// Summary of one capture run.
struct PerfSummary {
    std::size_t sampled = 0;   // frames included in the statistics
    double mean_ms = 0.0;
    double median_ms = 0.0;
    double p95_ms = 0.0;
    double max_ms = 0.0;
    double min_ms = 0.0;
};

// Fixed-capacity per-frame CPU time capture. Capacity is a compile-time constant and
// the plan's 600-frame minimum sits comfortably inside it; a longer run is truncated
// rather than growing.
class PerfCapture {
public:
    static constexpr std::size_t kCapacity = 4096;
    // The plan's floor for a valid measurement.
    static constexpr std::size_t kMinimumFrames = 600;
    // Frames discarded when sampling starts, before the first one is counted. Frame 0
    // pays for the font atlas upload and is what the plan says to exclude; the rest
    // absorb the first-frame batches of a newly entered stage. Constant for both runs.
    static constexpr std::size_t kWarmupFrames = 10;

    // Fixes the target and puts the capture on standby; nothing is sampled yet.
    void Arm(std::size_t target_frames) {
        target_frames_ = std::clamp<std::size_t>(target_frames, 1, kCapacity);
        count_ = 0;
        warmup_left_ = kWarmupFrames;
        armed_ = true;
        sampling_ = false;
    }

    // Starts sampling. Returns true only on the call that started it, so the caller can
    // log the moment (e.g. "capture started: stage playing") exactly once.
    bool BeginSampling() {
        if (!armed_ || sampling_) {
            return false;
        }
        sampling_ = true;
        return true;
    }

    bool Armed() const { return armed_; }
    bool Sampling() const { return sampling_; }
    std::size_t Count() const { return count_; }
    std::size_t Target() const { return target_frames_; }
    // True once enough frames have been recorded to stop and dump.
    bool Complete() const { return sampling_ && count_ >= target_frames_; }

    // Records one frame's CPU milliseconds. The first kWarmupFrames calls are dropped:
    // they are the frames that pay for the font atlas upload and the first draw setup.
    void Add(double cpu_ms) {
        if (!sampling_) {
            return;
        }
        if (warmup_left_ > 0) {
            --warmup_left_;
            return;
        }
        if (count_ >= kCapacity) {
            return;
        }
        samples_[count_++] = cpu_ms;
    }

    // mean / median / p95 / max over the recorded frames. Empty capture reports zeros.
    PerfSummary Summary() const {
        PerfSummary summary;
        if (count_ == 0) {
            return summary;
        }
        std::vector<double> sorted(samples_, samples_ + count_);
        std::sort(sorted.begin(), sorted.end());
        double total = 0.0;
        for (const double value : sorted) {
            total += value;
        }
        summary.sampled = count_;
        summary.mean_ms = total / static_cast<double>(count_);
        summary.min_ms = sorted.front();
        summary.max_ms = sorted.back();
        summary.median_ms = sorted[count_ / 2];
        const std::size_t p95_index =
            std::min(count_ - 1, static_cast<std::size_t>(0.95 * static_cast<double>(count_)));
        summary.p95_ms = sorted[p95_index];
        return summary;
    }

    // Writes "frame,cpu_ms" lines plus a trailing summary comment. Returns false when
    // the path cannot be written; the caller only warns.
    bool Dump(const std::string& path) const {
        std::FILE* file = nullptr;
#if defined(_MSC_VER)
#pragma warning(push)
#pragma warning(disable : 4996)  // std::fopen: This function or variable may be unsafe
#endif
        file = std::fopen(path.c_str(), "wb");
#if defined(_MSC_VER)
#pragma warning(pop)
#endif
        if (file == nullptr) {
            return false;
        }
        const PerfSummary summary = Summary();
        bool ok = std::fprintf(file, "# frames,mean_ms,median_ms,p95_ms,max_ms,min_ms\n") > 0;
        ok = ok && std::fprintf(file, "# %zu,%.4f,%.4f,%.4f,%.4f,%.4f\n", summary.sampled,
                                summary.mean_ms, summary.median_ms, summary.p95_ms, summary.max_ms,
                                summary.min_ms) > 0;
        ok = ok && std::fprintf(file, "frame,cpu_ms\n") > 0;
        for (std::size_t i = 0; ok && i < count_; ++i) {
            ok = std::fprintf(file, "%zu,%.4f\n", i, samples_[i]) > 0;
        }
        const bool closed = std::fclose(file) == 0;
        return ok && closed;
    }

private:
    bool armed_ = false;
    bool sampling_ = false;
    std::size_t warmup_left_ = kWarmupFrames;
    std::size_t target_frames_ = kMinimumFrames;
    std::size_t count_ = 0;
    double samples_[kCapacity] = {};
};

}  // namespace odyssey::client::ui
