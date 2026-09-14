# 角色 B：D8 恢复状态与结果验证

## 已验证行为

| 场景 | 结果 |
| --- | --- |
| 完整恢复状态 | ResumeState 经 Room 控制队列返回当前完整 Snapshot，不读取或替换 World |
| 奖励中恢复 | 只重建该玩家的待选 Options；已选择或超时默认时重建 Applied/Defaulted |
| 旧输入 | 同一 InputSeq 在恢复查询后仍被拒绝，不回滚位置或确认序号 |
| 私有与复制 | 修改恢复回执的玩家、怪物或奖励切片不会污染 World，也不会泄露队友选项 |
| GameResult | 胜利/失败/放弃与当前状态匹配；已清关历史、最终玩家属性和装备完整且按 ID 排序 |
| 终局稳定 | 团灭 EndedAtTick 冻结；后续 Tick 不改变结束时间或时长 |
| 关闭竞态 | Room 关闭会以 ErrClosed 完成已排队的 ResumeState/GameResult 回执 |

普通测试、全仓 test/vet 与 Linux race 必须通过。Token 单次消费、连接替换、首次协议快照发送、MySQL 幂等写入和真实断网恢复由 A/D/C 联调验收，本记录不代替这些外部结果。
