# Go SDK 接口说明

SDK 以 `Client` 为入口，按业务职责拆分为六个服务。所有写操作只提交 Kubernetes 期望状态；传入 `Wait=true` 时，SDK 继续观察 `BareMetalHost.status`，直到业务状态收敛或超时。

| 服务 | 作用 | 典型业务流程 |
|---|---|---|
| `Cluster()` | 探测 Kubernetes 与 BMO CRD 能力 | 启动时检查集群是否具备所需资源 |
| `Hosts()` | 登记、查询、更新、脱管/接管和删除物理机 | `Add` → 等待 `Available` → `Delete` 或 `Detach` |
| `Power()` | 管理开关机、普通重启和两阶段重启 | 维护前 `StartPhasedReboot`，完成后 `CompletePhasedReboot` |
| `Provisioning()` | 安装、重装、自定义部署和卸载系统 | `Install` → `Provisioned` → `Deprovision` |
| `Maintenance()` | 巡检、外部巡检数据、RAID 与固件 | 在 `Available/Ready` 阶段准备硬件；在线主机需配合 `HostUpdatePolicy` 与重启 |
| `Resources()` | 管理 BMO 辅助 CRD | 事件订阅、数据镜像、HostClaim 及部署/在线更新策略 |

## 关键业务约束

- BMC 用户名和密码只写入 SDK 管理的 Secret，不写入 `BareMetalHost` 明文字段。
- `Import` 只用于已从源集群 `Detach` 的主机迁移；导入为 detached 后必须显式 `Attach`。
- `Delete` 必须明确选择 `DeleteAndDeprovision` 或 `DeleteInventoryOnly`，避免误删仍在使用的主机。
- `Reboot` 只适用于 `Provisioned` 或 `ExternallyProvisioned` 主机；两阶段重启只释放调用方自己的 hold。
- 固件设置名称和值应先从 `GetFirmwareSchema` 获取；在线主机的固件更新通常应设置 `Wait=false`，通过 `HostUpdatePolicy` 在后续重启时执行。
- 通用 metadata 接口禁止 BMO 控制注解（如 reboot、detach、inspect、status），但允许普通扩展注解。

这些状态机约束对应 Metal³ 官方的 [BareMetalHost API 文档](https://github.com/metal3-io/baremetal-operator/blob/main/docs/api.md)、[重启注解说明](https://book.metal3.io/bmo/reboot_annotation) 和 [部署/卸载流程](https://book.metal3.io/bmo/provisioning.html)。
