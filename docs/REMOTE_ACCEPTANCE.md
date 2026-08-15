# 远端真实环境验收

验收日期：2026-08-15  
目标节点：`<remote-host>`  
原则：项目服务、Minikube 和 Ironic 都不在开发机启动。

## 现有环境

- Kubernetes `v1.32.4`，单节点 Minikube；
- BMO 提供 `metal3.io/v1alpha1` 的 11 类资源；
- IrSO 管理 Ironic `37.0`，现有数据库为独立 MariaDB + 8 GiB PVC，不是无数据库部署；
- 真实 BMH `<namespace>/<host>` 为 `OK/externally provisioned`，验收前后均保持开机；
- 远端不常驻 `go-metal3-api`：主要入口是集群外 Go SDK 使用 kubeconfig 直连 Kubernetes API。

## 集群外 Go SDK 验收

本项目的主要调用方式已在开发工作机真实验证：只运行本地 `metal3ctl` 客户端，通过 kubeconfig 连接目标 Kubernetes API，没有在本机运行 Minikube、Ironic 或 API 服务。结果如下：

- TLS 证书 SAN 包含目标 API 地址，客户端按 CA 正常校验；
- `Cluster.Info` 返回 Kubernetes `v1.32.4`，全部 11 类 BMO CRD 可发现；
- `Hosts.List(<namespace>)` 返回真实主机；
- `Hosts.Get(<namespace>/<host>)` 返回 `externally provisioned`、`operationalStatus=OK`、`poweredOn=true`；
- 读取真实主机的 HostFirmwareSettings 与 HostFirmwareComponents；
- 主机原本已经 `spec.online=true/status.poweredOn=true`，外部 SDK 调用幂等 `PowerOn(wait=true)` 返回 `Succeeded`，没有发生电源循环；
- 对真实主机 `spec.description` 完成临时写入、读取确认和原值恢复；最终 description 仍为空，主机仍为 `externally provisioned/OK/poweredOn=true`；
- 对真实主机完成 Detach 和 Attach 往返，Attach 后由 Ironic 重新登记并恢复 `externally provisioned/OK`；
- 对真实主机完成 PowerOff（约 32 秒）和 PowerOn（约 12 秒）往返；
- 对真实主机完成自动 Reboot（约 33 秒）以及两阶段 hard reboot（关机约 12 秒、总计约 25 秒），最终均恢复 `poweredOn=true/operationalStatus=OK`；
- 在隔离 namespace 中直接通过外部 Go SDK 完成 `Hosts.Import`、`Hosts.Update`、`DataImage` apply/list/delete 和 `Delete(InventoryOnly)` 写入往返；导入状态保持 `detached/externally provisioned`；
- 写入测试结束后 BMH、DataImage、自有 Secret 和 namespace 均已删除，Ironic node 总数仍为原有两台，没有为该测试新增 node；
- 临时 kubeconfig 和临时客户端二进制在测试结束时删除。


## 可选 HTTP API 的真实验收

以下结果用于证明可选 JSON/HTTP 适配层能够工作；它不是当前远端常驻组件。2026-08-15 清理后，远端 Deployment、Service、ConfigMap、Secret、ServiceAccount、Role 和 RoleBinding 均已删除，当前调用方式以集群外 Go SDK 为准。

清理后又在开发机临时启动当时源码的 `go-metal3-api`，使用同一份 `0600` kubeconfig 调用远端 Kubernetes API。`/healthz` 返回 200、未认证 cluster 请求返回 401；认证后发现 Kubernetes、Metal3 版本与 11 类 CRD 可正常发现，读取真实 BMH；幂等 `power-on(wait=true)` 返回 `Succeeded/power state converged`，没有触发电源循环。验收后本地进程已停止，API key 已删除，开发机仍无相关容器或镜像。

| 接口或行为 | 结果 |
|---|---|
| `/healthz` | 200 |
| 无 Bearer key 访问受保护接口 | 401 |
| Kubernetes/BMO discovery | 200，11 类 CRD 均可发现 |
| BMH list/get | 200，读取真实主机 |
| HostFirmwareSettings / HostFirmwareComponents | 200，读取真实 BIOS 与固件版本 |
| 不允许的 namespace | 404 |
| 已删除的 `configure-firmware` action | 404 |
| BMO admission 前置校验 | 无 Boot MAC 的禁用巡检请求由 SDK 返回 400，且不创建 Secret |

隔离 namespace 中还完成了写入往返：`Hosts.Import` 201、BMH metadata PATCH 200、DataImage PUT/list/delete、以及 InventoryOnly 删除。修复后，导入 detached status 会同时创建 status annotation 和 detached annotation；BMO 保持 `detached/externally provisioned`，Ironic 按名称查询为 404，最后 BMH 和 SDK 自有 Secret 均成功清理。

## 真实环境发现并修复的问题

1. distroless 镜像使用字符串用户时，Kubelet 无法仅凭 `runAsNonRoot` 验证 UID；部署清单已显式设置 UID/GID `65532`。
2. Ironic endpoint 同时接受 API root 和 `/v1/`，SDK 现在统一规范化，避免产生 `/v1/v1/nodes`。
3. BMO webhook 要求普通 BMC driver 提供 Boot MAC；禁用巡检后 Virtual Media driver 也必须提供。`disablePowerOff:true` 同时要求 `online:true`。SDK 现在会在创建 Secret 前返回 validation error。
4. 仅重建 detached status 不会让目标 BMO 保持脱管；`Hosts.Import` 现在自动同时设置 detached annotation，直到显式 `Attach`。
5. 真实自动重启第一次遇到 BMO 软关机失败并提示继续使用硬关机。旧 SDK 会把这个临时 `PowerManagementError` 当成终态而提前失败，但 BMO 随后实际完成硬关机回退。等待器现在只对 BMO 的精确回退消息继续等待，其他错误仍立即失败；修复后的自动重启和两阶段 hard reboot 均在真实主机通过。

## 独立功能边界

BMC 网页控制台不再属于 `go-metal3` SDK；其源码、部署、厂商适配和真实设备验收由独立项目维护。本文件只记录当前 SDK 对 Kubernetes/Metal3 API 的验收结果。
