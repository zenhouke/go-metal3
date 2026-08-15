# 实现状态

真实环境的运行验收、已修问题和未闭环运行项见 [`REMOTE_ACCEPTANCE.md`](REMOTE_ACCEPTANCE.md)。

本表区分 SDK 已实现能力和必须由具体 Metal3 部署提供的集成能力。编译或 fake client 测试通过不等同于真实 BMC/Ironic 操作已验收。

| 领域 | 状态 | 说明 |
|---|---|---|
| 外部 SDK 直连 Kubernetes API | 已实现并真实验收 | 本地客户端使用目标集群 kubeconfig；读测试覆盖 Cluster.Info/Hosts.List/Hosts.Get，隔离写测试覆盖 detached Import、Update、DataImage 和 InventoryOnly Delete，且未新增 Ironic node |
| Kubernetes 配置与 discovery | 已实现 | kubeconfig/in-cluster 加载、版本和 BMO v0.13 全部 11 个 CRD discovery |
| Host inventory | 已实现 | Add/Import/Get/List/Update/Delete、HardwareData、状态映射、显式 Detach/Attach、Available 后单向 AdoptExternallyProvisioned；Import 原子使用 status reconstruction annotation |
| BMC 管理 | 已实现 | Secret 创建/轮换、地址 detach-update-reattach、BMCEventSubscription CRUD |
| Power | 已实现并真实验收 | stable-state guard、PowerOn/PowerOff、soft/hard/force 简单重启，以及多客户端安全的 phased reboot start/complete；真实主机已通过关机、开机、自动重启和 hard phased reboot，并正确等待 BMO 的 soft-to-hard 回退 |
| Provisioning | 已实现 | Install、CustomDeploy、same-URL Reinstall、Deprovision、HTTP(S)/OCI/live-ISO 规则 |
| Instance config | 已实现 | user/network/meta immutable content-addressed Secrets，cloud-init/Ignition/network renderer |
| Inspection 与 Maintenance | 已实现 | `spec.inspectionMode`、available 主机重巡检、external inspection hardware details、RAID、HostFirmwareSettings、HostFirmwareComponents、预部署网络 Secret |
| BMO auxiliary resources | 已实现 | DataImage、HostClaim、HostDeployPolicy、HostUpdatePolicy typed CRUD |
| 结构化错误和等待 | 已实现 | stable codes、terminal BMO error、timeout、delete wait、conflict retry |
| 单元测试 | 已实现 | controller-runtime fake client 覆盖核心 lifecycle 和数据构造 |
| 可选 JSON API 适配层 | 已实现 | 供非 Go 调用方使用；TLS 前置校验、Bearer key、namespace allowlist、1 MiB strict JSON、生命周期路由；外部 Go SDK 直连模式不需要部署 |
| Minikube 集成清单 | 已实现，待目标网络验收 | BMO v0.13.2、Ironic 37、PVC 持久化内置 SQLite、Ingress、RBAC、Secret 和构建脚本 |
| 跨进程 Operation 持久化/Lease | 可选增强 | 单次同步工作流可用；多副本无状态 API 服务应外接数据库或 Operation CR/Lease |
| envtest/集成/E2E | 部分待目标环境验收 | 外部 SDK discovery、隔离写入、真实 BMC 电源、Detach/Attach 和重启已验收；真实镜像写盘和固件写入仍待验收 |

## 官方 API 对齐

- provisioning 从 `available` 且 `spec.image` 或 `customDeploy` 非空开始；完成以 `status.provisioning.state=provisioned` 为准。
- deprovision 清空 `image`、`userData`、`networkData`、`metaData`、`customDeploy`。
- 同 URL reprovision 不能只更新 user/network data，必须先移除 image，再重新提交。
- userData Secret 键为 `userData`，networkData Secret 键为 `networkData`。
- provisioned 与 externally provisioned 主机的 simple reboot 使用 `reboot.metal3.io`，BMO 恢复供电后移除 annotation。
- phased reboot 使用 SDK 生成的 `reboot.metal3.io/{operation-ID}`；complete 只删除本调用方 ID，对其他客户端 annotation 保持不变。
- inventory-only removal 和稳定态 BMC address 变更使用 `baremetalhost.metal3.io/detached` 并等待 `operationalStatus=detached`。
- 跨 BMO 集群迁移使用 `baremetalhost.metal3.io/status`，SDK 只允许稳定且无当前错误的状态，并在创建 BMH 的同一个请求中原子设置。
- `AdoptExternallyProvisioned` 只接受没有待部署 image/customDeploy 的 available/ready 主机；不提供 BMO 不支持的反向转换。
- 手动巡检仅接受 available/ready 主机；禁用巡检使用当前 `spec.inspectionMode`，而不是已弃用的 disabled annotation。
- 外部巡检数据通过专用 `inspect.metal3.io/hardwaredetails` action 提交；SDK 校验 CPU、内存、磁盘、NIC、IP/MAC/VLAN，并等待 BMO 消费 annotation。
- RAID 写入 BMH 后等待 preparing 流程重新收敛到 available。
- 任意 BIOS 设置写入同名 HostFirmwareSettings，并同时检查 `Valid=True`、`ChangeDetected=False` 和 status 值。
- BIOS/BMC/NIC 固件镜像写入同名 HostFirmwareComponents，并检查 `Valid=True`、`ChangeDetected=False` 和 status updates。
- `BareMetalHost.spec.firmware`/`FirmwareConfig` 已从 SDK 和 HTTP API 删除，因为 BMO v0.13 明确说明没有 driver 支持。
- provisioned 主机的 live settings/firmware update 先创建同名 HostUpdatePolicy `onReboot`，再以 `wait=false` 提交变更，最后 reboot 并等待 `operationalStatus=OK`。

Go 类型依赖与部署清单统一为 Metal3/BMO API module `v0.13.2`；不再使用缺少新 CRD 和 OCI auth 字段的 `v0.5.1`。

参考：Metal3 User Guide 的 BMO state machine、provisioning、instance customization、inspection annotation、RAID、firmware settings、preprovisioning network、reboot、detach、changing BMC address 和 version support 页面。

## BMO 用户指南覆盖矩阵

Metal3 API Reference 同时列出 BMO、Cluster API Provider Metal3（CAPM3）和 IP Address Manager（IPAM）。本项目的明确范围是直接操作 `BareMetalHost` 及 BMO `metal3.io/v1alpha1` 关联资源；CAPM3 的 Kubernetes 集群生命周期和 IPAM 地址分配不伪装成本 SDK 已实现能力。

| BMO 官方能力 | Go SDK | HTTP API | 静态证据状态 |
|---|---|---|---|
| 主机登记与状态机 | `Hosts().Add/Get/List/WaitForPhase` | hosts 查询/登记 | fake client 覆盖登记、Secret 和 phase 映射 |
| Provision/Deprovision/Reprovision | `Install/Reinstall/Deprovision` | install/reinstall/deprovision | 镜像、状态守卫、同 URL 两阶段重装测试 |
| Automated Cleaning | `HostCreateRequest`、`HostPatch` 的 cleaning mode | hosts POST/PATCH | 只允许 BMO 的 `metadata`/`disabled` |
| Automatic Secure Boot | `BootMode=UEFISecureBoot` | `bootMode` | 创建校验与 BMO 原生类型映射 |
| Inspection | `Inspect`、`SetInspectionMode` | inspect/inspection-mode | available 状态和 active inspection 守卫测试 |
| External Inspection | `SetExternalInspectionData` | external-inspection | annotation、硬件字段校验和路由测试 |
| HardwareData | `GetHardwareData` | hosts hardware | typed read API |
| RAID | `ConfigureRAID` | configure-raid | 硬件/软件互斥、删除语义和状态测试 |
| Firmware Settings | `Get/UpdateFirmwareSettings` | firmware-settings | Valid/ChangeDetected/status 收敛检查 |
| Firmware Updates | `Get/UpdateFirmwareComponents` | firmware-components | BIOS/BMC/NIC 名称和 URL 校验测试 |
| Live Updates/Servicing | `HostUpdatePolicy` + firmware APIs + reboot | host-update-policies + firmware + reboot | onPreparing/onReboot 校验和 servicing 等待 |
| 已废弃 FirmwareConfig | 无 | 无；旧 action 返回 404 | 无 driver 支持的 `spec.firmware` 已删除 |
| Instance Customization | Install/CustomDeploy 的 user/network/meta data | install/reinstall/custom-deploy | 内容寻址 immutable Secret 测试 |
| Pre-provisioning NetworkData | `SetPreprovisioningNetworkData` | preprovisioning-network-data | Secret 键、owner 和引用测试 |
| Root Device Hints | HostCreate/Install/CustomDeploy | 对应请求的 `rootDeviceHints` | BMO 原生 typed 映射 |
| Live ISO | `Install` 的 `format=live-iso` | install/reinstall | 禁止 checksum、磁盘提示和 config-drive 数据测试 |
| OCI Image | `Install` 的 `oci://`/auth Secret | install/reinstall | registry Secret 类型和存在性测试 |
| Simple Reboot | `Reboot` | reboot | auto/soft/hard/force annotation 测试 |
| Phased Reboot | `Start/CompletePhasedReboot` | phased-reboot-start/complete | 唯一 ID、关机确认、只删除本客户端 hold 测试 |
| Detach/Attach | `Hosts().Detach/Attach` | detach/attach | 稳定态、force、等待和 annotation 测试 |
| Changing BMC Address | `UpdateBMC` | update-bmc | detach-update-reattach 和 Secret 轮换测试 |
| Externally Provisioned | 创建时字段或 `AdoptExternallyProvisioned` | hosts POST/adopt-external | available 后单向转换测试 |
| Reconstructing Host Status | `Hosts().Import` | `POST /hosts/import` | 稳定状态、原子 annotation 和旧 firmware 拒绝 |
| BMC Events/DataImage/Claims/Policies | `Resources()` typed CRUD | 五类资源路由 | 验证器、fake client、RBAC 和 discovery 测试 |

以上“静态证据”只证明 SDK 转换、校验和清单结构；真实 BMC、IPA、镜像写盘、固件以及公网 DNS/NAT 仍必须在用户指定的远程环境验收。
