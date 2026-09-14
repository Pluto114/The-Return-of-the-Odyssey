# 当前协作入口：第二周 A / D 收尾

更新日期：2026-09-14。**当前 main 是统一联调基线，最终版本尚待验收。**

本轮完整任务、接口约定、实施顺序与验收标准统一在 **[A / D 收尾需求与最终合并验收](WEEK2-AD-FINALIZATION.md)**。原来的第一阶段接线清单已由该文档更新；不要继续按旧提交或“尚无客户端入口”等历史描述开工。

## 已整合的成果

| 角色 | 当前成果 | 下一步 |
| --- | --- | --- |
| A | 协议、TCP/Session、DTO、事件/快照分发和可靠队列饱和修复 | 正式首关、奖励、Ready/Director、恢复编排，牵头 C 跨端兼容收口 |
| B | 战斗/AI、装备药水、奖励、确定性 Director、多关核心、ResumeState/GameResult | 维护规则接口，复核 A/D 接入与性能证据 |
| C | 战斗显示、奖励界面、恢复状态机、预测插值与 CTest | 配合 A 修正阶段输入、Ready、恢复、装备/药水和运行端点；完成真实客户端验收 |
| D | 匹配、基础指标、Redis Token 模块、战斗 Bot 与战斗指标定义 | 配置装配、Bot 三关/恢复/持续负载、真实指标/管理端、Redis/MySQL 接入与验收归档 |

## 开工与交接

1. 先拉取本次 main，新功能分支从它创建；已有分支合入该基线后继续。保持个人未提交修改，不覆盖其他成员提交。
2. A 与 D 先确定配置、协议、恢复存储、事件观察和结算信封接口，再并行实现。B/C 分别复核领域规则与客户端兼容性。
3. A/D 各推送完成分支和验收记录；在同一个整合提交上跑完收尾文档 F01–F09，随后合并最终版本并按计划发布候选标签。

本次合入来源和实际检查见 [整合验证记录](../verification/week2-main-integration/README.md)。编译与模块测试通过不代表真实三关、数据库或持续负载已经通过。

## 参考

- [环境安装与配置](../SETUP.md)、[客户端构建与操作](../../client/README.md)
- [第二周 D4–D10 计划](WEEK2-DAYS4-10.md)、[前三天计划](PHASE1-DAYS1-3.md)
- [战斗契约](../architecture/COMBAT-CORE.md)、[装备与奖励](../architecture/EQUIPMENT-REWARD.md)
- [Director](../architecture/DIRECTOR.md)、[恢复与结算](../architecture/RESUME-GAME-RESULT.md)
- [历史 A/B 集成验证](../verification/network-core/README.md)、[核心性能基线](../benchmark/WEEK2-B-D9.md)
