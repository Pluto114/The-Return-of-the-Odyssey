// Client-side reward (treasure chest) view for D6.
//
// The server owns reward legality: it sends RewardOptions (equipment ids +
// a deadline tick), the client submits only a candidate equipment_id, and the
// server answers with RewardApplied(reason, equipment_id). The client never
// invents stat effects - it shows them from the static equipment table and
// from the next authoritative snapshot.
//
// Static equipment data is NOT sent over the wire; the client keeps a local
// display table (see ParseEquipmentTable) that is copied from the versioned
// repository config at build time. Missing entries degrade to "equipment#<id>".
#pragma once

#include <cstdint>
#include <map>
#include <sstream>
#include <string>
#include <vector>

namespace odyssey::client::sync {

struct EquipmentDisplay {
    std::uint32_t id = 0;
    std::string name;
    std::string slot;
    std::string stats;  // pre-formatted display text, e.g. "+10 Attack, +5% Speed"
};

using EquipmentTable = std::map<std::uint32_t, EquipmentDisplay>;

// Parses "id,name,slot,stats" lines; '#' starts a comment; blank lines are
// ignored; the stats column may itself contain commas.
inline std::size_t ParseEquipmentTable(const std::string& text, EquipmentTable& out) {
    std::size_t loaded = 0;
    std::istringstream stream(text);
    std::string line;
    while (std::getline(stream, line)) {
        if (!line.empty() && line.back() == '\r') {
            line.pop_back();
        }
        const auto first = line.find_first_not_of(" \t");
        if (first == std::string::npos || line[first] == '#') {
            continue;
        }
        std::vector<std::string> fields;
        std::string current;
        std::istringstream line_stream(line);
        for (int i = 0; i < 3 && std::getline(line_stream, current, ','); ++i) {
            fields.push_back(current);
        }
        std::string stats;
        std::getline(line_stream, stats);  // remainder (may contain commas)
        if (fields.size() < 2) {
            continue;
        }
        EquipmentDisplay display;
        try {
            display.id = static_cast<std::uint32_t>(std::stoul(fields[0]));
        } catch (...) {
            continue;
        }
        display.name = fields[1];
        if (fields.size() > 2) {
            display.slot = fields[2];
        }
        display.stats = stats;
        out[display.id] = display;
        ++loaded;
    }
    return loaded;
}

enum class RewardState {
    kNone,      // no active reward
    kOffered,   // options shown, awaiting our choice
    kChosen,    // choice sent, awaiting RewardApplied
    kApplied,   // server applied it (REASON_OK)
    kRejected,  // server refused (reason kept in note)
    kTimedOut,  // local deadline passed before we chose
};

struct RewardOption {
    std::uint32_t id = 0;
    EquipmentDisplay display;
};

class RewardView {
public:
    void SetOptions(const std::vector<std::uint32_t>& ids, std::uint64_t deadline_tick,
                    const EquipmentTable& table) {
        options_.clear();
        for (const auto id : ids) {
            RewardOption option;
            option.id = id;
            const auto it = table.find(id);
            if (it != table.end()) {
                option.display = it->second;
            } else {
                option.display.id = id;
                option.display.name = "equipment#" + std::to_string(id);
                option.display.slot = "(config pending)";
            }
            options_.push_back(option);
        }
        deadline_tick_ = deadline_tick;
        chosen_id_ = 0;
        state_ = options_.empty() ? RewardState::kNone : RewardState::kOffered;
        note_ = options_.empty() ? "no options" : "choose 1-" + std::to_string(options_.size());
    }

    bool Active() const { return state_ == RewardState::kOffered || state_ == RewardState::kChosen; }
    RewardState State() const { return state_; }
    const std::string& Note() const { return note_; }
    std::uint64_t DeadlineTick() const { return deadline_tick_; }
    std::uint32_t ChosenId() const { return chosen_id_; }
    const std::vector<RewardOption>& Options() const { return options_; }

    // Valid only while kOffered; index is 0-based into Options().
    bool ChooseByIndex(std::size_t index, std::uint32_t& equipment_out) {
        if (state_ != RewardState::kOffered || index >= options_.size()) {
            return false;
        }
        equipment_out = options_[index].id;
        chosen_id_ = equipment_out;
        state_ = RewardState::kChosen;
        note_ = "choice sent: " + std::to_string(equipment_out);
        return true;
    }

    void ApplyResult(bool ok, std::uint32_t equipment_id, std::uint32_t reason) {
        if (ok) {
            state_ = RewardState::kApplied;
            chosen_id_ = equipment_id;
            note_ = "applied " + std::to_string(equipment_id);
        } else {
            state_ = RewardState::kRejected;
            note_ = "refused id=" + std::to_string(equipment_id) +
                    " reason=" + std::to_string(reason);
        }
    }

    // Called when the authoritative server tick passes the deadline. The
    // server applies its default; we only stop accepting input.
    void Timeout() {
        if (state_ == RewardState::kOffered) {
            state_ = RewardState::kTimedOut;
            note_ = "deadline passed (server default applies)";
        }
    }

    // The authoritative stage state already says the reward phase ended (the
    // server completes the round itself), but this client is still waiting - a
    // missed or reordered RewardApplied must not leave the panel open and block
    // the ready barrier. The server owns the outcome, so nothing is invented
    // here beyond "it is over".
    void SettleAfterAuthoritativeEnd() {
        if (state_ == RewardState::kOffered || state_ == RewardState::kChosen) {
            state_ = RewardState::kTimedOut;
            note_ = "reward phase ended (server settled it)";
        }
    }

    void Clear() {
        options_.clear();
        deadline_tick_ = 0;
        chosen_id_ = 0;
        state_ = RewardState::kNone;
        note_.clear();
    }

private:
    std::vector<RewardOption> options_;
    std::uint64_t deadline_tick_ = 0;
    std::uint32_t chosen_id_ = 0;
    RewardState state_ = RewardState::kNone;
    std::string note_;
};

}  // namespace odyssey::client::sync
