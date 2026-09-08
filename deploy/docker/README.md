# 应用镜像预留

当前 Compose 只运行 MySQL、Redis、Prometheus、Grafana。
gameserver / bot 尚无 main 入口，故此阶段不创建无法构建的应用 Dockerfile。
成员 D 在应用入口完成后补充 Linux 多阶段构建、非 root 用户与健康检查。
