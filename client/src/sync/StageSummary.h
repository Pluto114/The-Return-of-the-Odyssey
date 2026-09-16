// Per-stage difficulty / global-modifier summary for the HUD (D7).
//
// Where the data can come from: the reliable stage EVENTS (MSG_STAGE_STARTED_EVENT 325 /
// MSG_STAGE_CLEARED_EVENT 326) carry ONLY stage_index + server_tick. The global modifiers,
// the difficulty score and the clear time live on A's domain messages MSG_STAGE_STARTED
// (400) / MSG_STAGE_CLEARED (401). Until the server sends those, this class stays empty
// and every caller hides its line: an unpopulated message must never render as
// "DIFF 0" or as an invented modifier.
//
// Protocol- and raylib-free so the formatting rules can be unit tested, and allocation-free
// (fixed storage + snprintf) so the render loop can call the formatters directly.
#pragma once

#include <cstddef>
#include <cstdint>
#include <cstdio>

namespace odyssey::client::sync {

// Wire enum values (common.proto StatModifier). Named here so the formatting rules read as
// intent instead of magic numbers; unknown values are still handled, never assumed.
inline constexpr std::uint32_t kModTargetPlayer = 1;
inline constexpr std::uint32_t kModTargetMonster = 2;
inline constexpr std::uint32_t kModOpAdd = 1;
inline constexpr std::uint32_t kModOpMultiply = 2;
inline constexpr std::uint32_t kModStatAttack = 1;
inline constexpr std::uint32_t kModStatDefense = 2;
inline constexpr std::uint32_t kModStatMaxHealth = 3;
inline constexpr std::uint32_t kModStatMoveSpeed = 4;
inline constexpr std::uint32_t kModStatAttackSpeed = 5;

// One global modifier as the UI shows it.
struct StageModifier {
    std::uint32_t target = 0;
    std::uint32_t op = 0;
    std::uint32_t stat = 0;
    float value = 0.0f;
};

class StageSummary {
public:
    // The HUD line shows at most this many; the rest is reported as a count.
    static constexpr std::size_t kMaxShown = 4;

    // Session change (fresh connect / reconnect / new match): forget everything.
    void Clear() { *this = StageSummary{}; }

    // A new stage begins. Idempotent per index on purpose: the reliable event (325) and
    // A's detail message (400) may arrive in either order, and whichever lands second must
    // not wipe what the other already delivered. The seed survives, because a stage's seed
    // is stable and the snapshot refreshes it within a tick or two.
    void BeginStage(std::uint32_t index) {
        if (has_index_ && index == index_) {
            return;
        }
        index_ = index;
        has_index_ = true;
        monster_count_ = 0;
        has_monster_count_ = false;
        modifier_count_ = 0;
        modifier_overflow_ = 0;
        has_difficulty_ = false;
        difficulty_score_ = 0;
        has_clear_time_ = false;
        clear_time_ms_ = 0;
    }

    void SetMonsterCount(std::uint32_t count) {
        monster_count_ = count;
        has_monster_count_ = true;
    }

    void SetSeed(std::int64_t seed) {
        seed_ = seed;
        has_seed_ = true;
    }

    void AddModifier(std::uint32_t target, std::uint32_t op, std::uint32_t stat, float value) {
        if (modifier_count_ >= kMaxShown) {
            ++modifier_overflow_;
            return;
        }
        modifiers_[modifier_count_] = StageModifier{target, op, stat, value};
        ++modifier_count_;
    }

    // Stage cleared. Zero means the server did not fill the field (the emitters send only
    // the index today), so a zero is treated as "no data" rather than as a real value.
    void SetCleared(std::uint32_t difficulty_score, std::uint64_t clear_time_ms) {
        if (difficulty_score > 0) {
            has_difficulty_ = true;
            difficulty_score_ = difficulty_score;
        }
        if (clear_time_ms > 0) {
            has_clear_time_ = true;
            clear_time_ms_ = clear_time_ms;
        }
    }

    bool has_index() const { return has_index_; }
    std::uint32_t index() const { return index_; }
    bool has_monster_count() const { return has_monster_count_; }
    std::uint32_t monster_count() const { return monster_count_; }
    bool has_seed() const { return has_seed_; }
    std::int64_t seed() const { return seed_; }
    std::size_t modifier_count() const { return modifier_count_; }
    std::size_t modifier_overflow() const { return modifier_overflow_; }
    bool has_modifiers() const { return modifier_count_ > 0; }
    const StageModifier& modifier(std::size_t i) const { return modifiers_[i]; }
    bool has_difficulty() const { return has_difficulty_; }
    std::uint32_t difficulty_score() const { return difficulty_score_; }
    bool has_clear_time() const { return has_clear_time_; }
    std::uint64_t clear_time_ms() const { return clear_time_ms_; }

    // True when there is anything beyond the plain index/state line to show.
    bool HasDetail() const { return has_difficulty_ || has_clear_time_ || modifier_count_ > 0; }

    static const char* StatName(std::uint32_t stat) {
        switch (stat) {
            case kModStatAttack: return "ATK";
            case kModStatDefense: return "DEF";
            case kModStatMaxHealth: return "HP";
            case kModStatMoveSpeed: return "SPD";
            case kModStatAttackSpeed: return "ASPD";
            default: return nullptr;
        }
    }

    static const char* TargetName(std::uint32_t target) {
        switch (target) {
            case kModTargetPlayer: return "PLAYER";
            case kModTargetMonster: return "MONSTER";
            default: return nullptr;
        }
    }

    // One modifier as ASCII, e.g. "ATK +20%", "SPD -10%", "HP +25", "MONSTER ATK +5".
    // An add is printed as a signed delta; a multiply is printed as a percentage, matching
    // the server's semantics (`*= value`, so 1.2 means +20%). An unknown stat keeps its raw
    // id ("STAT7 +2") and an unknown operation falls back to a plain value, so a newer
    // server is still readable instead of being silently dropped.
    // Returns the number of characters written, or 0 when there is nothing to show.
    std::size_t FormatModifier(std::size_t i, char* out, std::size_t size,
                               bool with_target = false) const {
        if (out == nullptr || size == 0 || i >= modifier_count_) {
            return 0;
        }
        out[0] = '\0';
        const StageModifier& modifier = modifiers_[i];
        char stat_buf[16] = {0};
        const char* stat = StatName(modifier.stat);
        if (stat == nullptr) {
            std::snprintf(stat_buf, sizeof(stat_buf), "STAT%u", modifier.stat);
            stat = stat_buf;
        }
        const char* target = with_target ? TargetName(modifier.target) : nullptr;
        const bool percent = modifier.op == kModOpMultiply;
        // Substituted through %s, so this is a literal percent sign - not a format escape.
        const char* suffix = percent ? "%" : "";
        const double shown = percent ? (static_cast<double>(modifier.value) - 1.0) * 100.0
                                     : static_cast<double>(modifier.value);
        const int written = target != nullptr
                                ? std::snprintf(out, size, "%s %s %+.4g%s", target, stat, shown,
                                                suffix)
                                : std::snprintf(out, size, "%s %+.4g%s", stat, shown, suffix);
        if (written <= 0 || static_cast<std::size_t>(written) >= size) {
            out[0] = '\0';
            return 0;
        }
        return static_cast<std::size_t>(written);
    }

    // "ATK +20%, SPD +10%" plus " (+2 more)" when the list overflowed. 0 when empty.
    std::size_t FormatModifiers(char* out, std::size_t size) const {
        if (out == nullptr || size == 0) {
            return 0;
        }
        out[0] = '\0';
        std::size_t used = 0;
        for (std::size_t i = 0; i < modifier_count_; ++i) {
            char part[64] = {0};
            if (FormatModifier(i, part, sizeof(part)) == 0) {
                continue;
            }
            if (used > 0 && !AppendText(out, size, used, ", ")) {
                return 0;
            }
            if (!AppendText(out, size, used, part)) {
                return 0;
            }
        }
        if (used > 0 && modifier_overflow_ > 0) {
            char more[32] = {0};
            std::snprintf(more, sizeof(more), " (+%zu more)", modifier_overflow_);
            if (!AppendText(out, size, used, more)) {
                return 0;
            }
        }
        return used;
    }

    // "DIFF 3   CLEAR 42.5s" - only the parts that actually arrived. 0 when neither did.
    std::size_t FormatCleared(char* out, std::size_t size) const {
        if (out == nullptr || size == 0) {
            return 0;
        }
        out[0] = '\0';
        std::size_t used = 0;
        if (has_difficulty_) {
            char part[32] = {0};
            std::snprintf(part, sizeof(part), "DIFF %u", difficulty_score_);
            if (!AppendText(out, size, used, part)) {
                return 0;
            }
        }
        if (has_clear_time_) {
            char part[40] = {0};
            std::snprintf(part, sizeof(part), "CLEAR %.1fs",
                          static_cast<double>(clear_time_ms_) / 1000.0);
            if (used > 0 && !AppendText(out, size, used, "   ")) {
                return 0;
            }
            if (!AppendText(out, size, used, part)) {
                return 0;
            }
        }
        return used;
    }

    // The single HUD line: difficulty, clear time and the modifier list joined by ", ".
    // 0 when there is nothing to show, so the caller can skip the draw entirely.
    std::size_t FormatLine(char* out, std::size_t size) const {
        if (out == nullptr || size == 0) {
            return 0;
        }
        char cleared[96] = {0};
        char modifiers[192] = {0};
        const std::size_t cleared_len = FormatCleared(cleared, sizeof(cleared));
        const std::size_t modifiers_len = FormatModifiers(modifiers, sizeof(modifiers));
        if (cleared_len == 0 && modifiers_len == 0) {
            out[0] = '\0';
            return 0;
        }
        out[0] = '\0';
        std::size_t used = 0;
        if (cleared_len > 0 && !AppendText(out, size, used, cleared)) {
            return 0;
        }
        if (modifiers_len > 0) {
            if (used > 0 && !AppendText(out, size, used, "   ")) {
                return 0;
            }
            if (!AppendText(out, size, used, modifiers)) {
                return 0;
            }
        }
        return used;
    }

private:
    // Appends text at the cursor; false when it would not fit, so a caller can report
    // "nothing" instead of a clipped line.
    static bool AppendText(char* out, std::size_t size, std::size_t& used, const char* text) {
        const int written = std::snprintf(out + used, size - used, "%s", text);
        if (written < 0 || static_cast<std::size_t>(written) >= size - used) {
            return false;
        }
        used += static_cast<std::size_t>(written);
        return true;
    }

    bool has_index_ = false;
    std::uint32_t index_ = 0;
    bool has_monster_count_ = false;
    std::uint32_t monster_count_ = 0;
    bool has_seed_ = false;
    std::int64_t seed_ = 0;
    StageModifier modifiers_[kMaxShown] = {};
    std::size_t modifier_count_ = 0;
    std::size_t modifier_overflow_ = 0;
    bool has_difficulty_ = false;
    std::uint32_t difficulty_score_ = 0;
    bool has_clear_time_ = false;
    std::uint64_t clear_time_ms_ = 0;
};

}  // namespace odyssey::client::sync
