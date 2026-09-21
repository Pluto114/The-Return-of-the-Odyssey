奥德赛归途 · 动态远征版（Windows x64）

解压整个目录后运行，不能单独复制 exe。服务端与两台客户端须同时升级。
start-lan.cmd：连接目前的局域网服务器 10.22.31.251:7777。
start-radmin.cmd：连接目前的 Radmin 地址 26.127.0.254:7777。
服务器地址有变化时，在本目录 PowerShell 执行：
powershell -ExecutionPolicy Bypass -File .\start-custom.ps1 -ServerHost 服务器IP
本机服务器可直接双击 odyssey_client.exe（默认 127.0.0.1:7777）。

默认十二关；WASD 移动，鼠标瞄准，空格射击，R 换弹（12 发 / 1.5 秒）。
铁制/速射/弹鼓手枪弹匣分别为 16/18/24 发；武器替换不叠加，扩容后需换弹。
Q 使用药水；清关选奖励后双方按回车；终局 N 再玩一把。
F4 完整/简化特效，F3 诊断。导演难度与上关表现显示在左下方。
更多操作见 PLAYER-GUIDE.md。

请确认标题为“奥德赛归途 · 动态远征 · 积分榜”。
如提示缺少 VCRUNTIME140/MSVCP140，请从微软官方安装 VC++ 2015–2022/后续版本 x64 运行库。
不要关闭整个防火墙；服务端应只放行游戏 TCP 端口。
