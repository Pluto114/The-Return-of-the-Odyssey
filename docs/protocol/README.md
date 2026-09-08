# 协议工作区

唯一真相源为根目录 proto/，六个文件只声明 proto3、包名和 go_package，尚未定义消息。

```powershell
pwsh -File scripts/generate-proto/generate.ps1
```

输出到 server/generated/protocol/ 与 client/generated/protocol/；Bot 直接导入服务端的公开 generated 包。
脚本校验 protoc 33.4，并将 protoc-gen-go v1.36.12 安装到项目 .tools/bin/。
必须使用与 vcpkg 中 C++ runtime 对应的 protoc，不能直接使用系统 PATH 中另一版本。

架构约定：16 字节 Header，大端序；Magic 建议 0x4E52、Version 1。
Header 依次为 Magic(2)、Version(1)、Flags(1)、MessageType(2)、Reserved(2)、BodyLength(4)、Sequence(4)。
Frame Sequence 与 PlayerInput 的 Input Sequence 分开。

| 预留范围 | 分组 | 文件 |
| --- | --- | --- |
| 0–99 | System | system.proto |
| 100–199 | Login / Session | session.proto |
| 200–299 | Lobby / Match | lobby.proto |
| 300–399 | Game | game.proto |
| 400–499 | Stage / Reward | stage.proto |

common.proto 预留跨消息公共 DTO。具体 ID、字段和验证规则由 A/C 下一阶段评审。
已经发布的字段编号不得复用；删除字段用 reserved。不要让 Domain Model 依赖 protobuf 对象。
