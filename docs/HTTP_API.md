# 可选 HTTP JSON API

`cmd/go-metal3-api` 是把 Go SDK 转换为 JSON/HTTP 的可选适配层，不是 SDK 工作所必需的组件。Go 程序可以直接把 kubeconfig 或 `*rest.Config` 传给 `metal3sdk.New`，通过 Kubernetes API 操作 Metal3；测试采用的外部 SDK 入口为目标集群 API，不要求公网域名、Ingress 或对外部署本 HTTP 服务。

只有需要让非 Go 调用方使用 JSON API 时，才部署 `cmd/go-metal3-api`。除 `/healthz` 和 `/readyz` 外，所有接口都要求：

```text
Authorization: Bearer <至少 32 字符的 API key>
```

服务启动时还必须配置 `MANAGED_NAMESPACES`。请求中的 namespace 不在 allowlist 时返回 404；部署清单同时只在 `baremetal-operator-system` namespace 绑定 Role，不授予集群级 Secret 权限。

## 路由

| 方法 | 路径 | 功能 |
|---|---|---|
| `GET` | `/api/v1/cluster` | Kubernetes 版本及 BMO v0.13 全部 11 个 CRD discovery |
| `GET` | `/api/v1/hosts?namespace=...&label=k=v` | 查询主机；`label` 可重复 |
| `POST` | `/api/v1/hosts` | 登记 BMH 与 BMC Secret |
| `POST` | `/api/v1/hosts/import` | 从其他 BMO 集群原子导入 BMH 与已知 status |
| `GET` | `/api/v1/hosts/{namespace}/{name}` | 查询 BMH |
| `PATCH` | `/api/v1/hosts/{namespace}/{name}` | 更新 labels、非控制类 annotations、description、自动清理模式 |
| `GET` | `/api/v1/hosts/{namespace}/{name}/hardware` | 查询 HardwareData |
| `GET` | `/api/v1/hosts/{namespace}/{name}/firmware-settings` | 查询 HostFirmwareSettings |
| `PATCH` | `/api/v1/hosts/{namespace}/{name}/firmware-settings` | 更新任意 BIOS 设置 |
| `GET` | `/api/v1/hosts/{namespace}/{name}/firmware-components` | 查询 BIOS/BMC/NIC 固件版本与升级状态 |
| `PATCH` | `/api/v1/hosts/{namespace}/{name}/firmware-components` | 提交 BIOS/BMC/NIC 固件镜像升级 |
| `GET` | `/api/v1/hosts/{namespace}/{name}/preprovisioning-image` | 查询同名 PreprovisioningImage |
| `GET` | `/api/v1/firmware-schemas/{namespace}/{name}` | 查询 FirmwareSchema |
| `POST` | `/api/v1/hosts/{namespace}/{name}/actions/power-on` | 开机 |
| `POST` | `/api/v1/hosts/{namespace}/{name}/actions/power-off` | 关机 |
| `POST` | `/api/v1/hosts/{namespace}/{name}/actions/reboot` | 重启 |
| `POST` | `/api/v1/hosts/{namespace}/{name}/actions/phased-reboot-start` | 开始两阶段重启并保持关机 |
| `POST` | `/api/v1/hosts/{namespace}/{name}/actions/phased-reboot-complete` | 删除本调用方 hold，允许两阶段重启继续开机 |
| `POST` | `/api/v1/hosts/{namespace}/{name}/actions/detach` | 从 Ironic 管理安全脱离，可选 force |
| `POST` | `/api/v1/hosts/{namespace}/{name}/actions/attach` | 重新交给 BMO/Ironic 管理 |
| `POST` | `/api/v1/hosts/{namespace}/{name}/actions/adopt-external` | 将已巡检的 available 主机接管为 externally provisioned |
| `POST` | `/api/v1/hosts/{namespace}/{name}/actions/install` | 安装系统 |
| `POST` | `/api/v1/hosts/{namespace}/{name}/actions/custom-deploy` | 执行 site-specific customDeploy method |
| `POST` | `/api/v1/hosts/{namespace}/{name}/actions/reinstall` | 同 URL 也会先卸载再安装 |
| `POST` | `/api/v1/hosts/{namespace}/{name}/actions/deprovision` | 卸载系统 |
| `POST` | `/api/v1/hosts/{namespace}/{name}/actions/update-bmc` | 安全更新 BMC 地址/凭据 |
| `POST` | `/api/v1/hosts/{namespace}/{name}/actions/inspect` | 对 available 主机重新执行巡检 |
| `POST` | `/api/v1/hosts/{namespace}/{name}/actions/external-inspection` | 提交外部采集的硬件信息 |
| `POST` | `/api/v1/hosts/{namespace}/{name}/actions/inspection-mode` | 启用/禁用自动巡检 |
| `POST` | `/api/v1/hosts/{namespace}/{name}/actions/configure-raid` | 配置硬件或软件 RAID |
| `POST` | `/api/v1/hosts/{namespace}/{name}/actions/preprovisioning-network-data` | 设置/清除预部署网络 Secret |
| `DELETE` | `/api/v1/hosts/{namespace}/{name}?mode=...` | 显式选择 `Deprovision` 或 `InventoryOnly` 后删除 |
| `GET/POST` | `/api/v1/bmc-event-subscriptions` | 按 namespace 查询/创建 BMC webhook subscription |
| `GET/DELETE` | `/api/v1/bmc-event-subscriptions/{namespace}/{name}` | 查询/删除 subscription |
| `GET` | `/api/v1/data-images?namespace=...` | 查询 DataImage |
| `GET/PUT/DELETE` | `/api/v1/data-images/{namespace}/{name}` | 查询、apply 或移除附加数据镜像 |
| `GET/POST` | `/api/v1/host-claims` | 查询/创建 HostClaim |
| `GET/DELETE` | `/api/v1/host-claims/{namespace}/{name}` | 查询/释放 HostClaim |
| `GET` | `/api/v1/host-deploy-policies?namespace=...` | 查询 HostDeployPolicy |
| `GET/PUT/DELETE` | `/api/v1/host-deploy-policies/{namespace}/{name}` | 查询、apply 或删除跨 namespace claim policy |
| `GET` | `/api/v1/host-update-policies?namespace=...` | 查询 HostUpdatePolicy |
| `GET/PUT/DELETE` | `/api/v1/host-update-policies/{namespace}/{name}` | 查询、apply 或删除 live-update policy |

## 本次新增或补齐了什么

| 能力 | 对外接口 | 中文说明 |
|---|---|---|
| 全量 CRD 探测 | `GET /api/v1/cluster` | 返回 BMO v0.13 的 11 类资源是否存在、是否属于 namespace，以及允许的 Kubernetes 操作 |
| 脱管与接管 | `detach`、`attach` | 暂停或恢复 BMO/Ironic 对物理机的管理 |
| 强制重启 | `reboot` 的 `force` 字段 | 普通重启无法完成时请求强制执行；可能造成业务中断 |
| 两阶段重启 | `phased-reboot-start`、`phased-reboot-complete` | 让主机在维护窗口保持关机，所有客户端释放自己的 hold 后才重新开机 |
| OCI 私有镜像 | `install/reinstall` 的 `ociAuthSecretName` | 指定同 namespace 已存在的镜像仓库认证 Secret |
| 自定义部署 | `custom-deploy` | 调用站点 ramdisk 提供的自定义部署步骤，不是普通镜像安装 |
| 当前固件资源 | `firmware-settings`、`firmware-components` | 分别管理 BIOS 设置，以及 BIOS/BMC/NIC 固件镜像 |
| BMC 事件订阅 | `/api/v1/bmc-event-subscriptions` | 让 BMC 将 Redfish 事件发送到 webhook |
| 数据镜像 | `/api/v1/data-images` | 管理部署过程中使用的附加数据镜像 |
| 主机认领 | `/api/v1/host-claims` | 按条件选择和占用一台可用 BareMetalHost |
| 认领范围策略 | `/api/v1/host-deploy-policies` | 控制哪些 namespace 可以跨 namespace 认领主机 |
| 在线更新策略 | `/api/v1/host-update-policies` | 控制 BIOS 设置和固件升级在 preparing 或 reboot 时执行 |
| 跨集群导入 | `POST /api/v1/hosts/import` | 在创建 BMH 时原子重建已知状态，避免重复巡检或重新部署 |
| 外部巡检数据 | `external-inspection` | 把其他系统采集的 CPU、内存、磁盘和网卡信息交给 BMO |
| 外部已部署接管 | `adopt-external` | 对已登记并巡检完成的 available 主机单向设置 externally provisioned |

`BareMetalHost.spec.firmware` 和旧 `FirmwareConfig` 已删除，因为没有 driver 支持。它们不是上表中仍受支持的 `HostFirmwareSettings` 和 `HostFirmwareComponents`。

## 通用字段

| JSON 字段 | 中文含义 |
|---|---|
| `namespace` | Kubernetes 命名空间；只能使用服务端 `MANAGED_NAMESPACES` 允许的值 |
| `name` | Kubernetes 资源名 |
| `labels` | 用于筛选和匹配资源的 Kubernetes 标签 |
| `annotations` | Kubernetes 注解；重启、巡检、detach 等控制类注解不能从通用接口直接写入 |
| `wait` | `false` 只提交操作，`true` 会继续观察 BMH，直到完成、失败或超时 |
| `timeoutSeconds` | `wait:true` 时的最长等待秒数 |

Kubernetes 接受一次更新只代表操作已经提交，不代表物理机已经执行完成。需要同步确认结果时使用 `wait:true`，否则应继续查询 BareMetalHost 的 `status`。

## 登记物理机字段

`POST /api/v1/hosts`

| 字段 | 必填 | 中文含义 |
|---|---:|---|
| `namespace` | 是 | 创建 BareMetalHost 和 BMC Secret 的命名空间 |
| `name` | 是 | 物理机在 Kubernetes 中的名称 |
| `bmcAddress` | 是 | BMC 连接地址，例如 Redfish Virtual Media 地址或 IPMI 地址 |
| `bmcUsername` | 是 | BMC 用户名；保存到 Secret，不写入 BMH 明文字段 |
| `bmcPassword` | 是 | BMC 密码；保存到 Secret，不应出现在日志中 |
| `bootMACAddress` | 视 driver 而定 | 普通 IPMI/Redfish driver 必填；Virtual Media driver 只有在启用自动巡检时才可省略。禁用巡检后所有 driver 都必须提供 |
| `bootMode` | 否 | 启动模式：`UEFI`、`UEFISecureBoot` 或 `legacy` |
| `online` | 否 | 是否要求主机开机 |
| `architecture` | 否 | CPU 架构提示，例如 `x86_64` 或 `aarch64` |
| `rootDeviceHints` | 否 | 选择系统安装目标磁盘，子字段见下表 |
| `inspectionDisabled` | 否 | 为 `true` 时禁用自动硬件巡检 |
| `externallyProvisioned` | 否 | 系统由 Metal3 之外的平台安装，BMO 只接管有限能力 |
| `automatedCleaningMode` | 否 | 自动清理策略，通常使用 `metadata` 或 `disabled` |
| `disableCertificateValidation` | 否 | 跳过 BMC HTTPS 证书校验；只适合可信网络中的自签名证书 |
| `disablePowerOff` | 否 | 删除或卸载时禁止 BMO 自动关机；设置为 `true` 时 `online` 也必须为 `true` |
| `preprovisioningNetworkData` | 否 | inspection/provisioning ramdisk 使用的网络配置文本 |
| `consumerRef` | 否 | 指向正在使用这台主机的 Kubernetes 对象，例如 Cluster API Machine |
| `taints` | 否 | BMH 上的调度污点信息 |
| `description` | 否 | 便于管理员识别主机的说明 |

`rootDeviceHints` 用于从多块磁盘中选择系统盘：

| 子字段 | 中文含义 |
|---|---|
| `deviceName` | 精确设备名，例如 `/dev/sda` |
| `serialNumber` | 精确匹配磁盘序列号，通常比 `/dev/sdX` 稳定 |
| `wwn` | 精确匹配磁盘 WWN |
| `model`、`vendor` | 按型号或厂商名称的子串匹配 |
| `minSizeGigabytes` | 磁盘最小容量，单位 GB |
| `rotational` | `true` 选择机械盘，`false` 选择非机械盘 |

## 跨集群导入字段

`POST /api/v1/hosts/import` 只用于把已经由 BMO 管理的主机迁移到另一集群。必须先在源集群执行 `detach` 并等待 `operationalStatus=detached`，再从源 BMH 导出完整 `status`。不能对一台仍由源集群管理的主机直接调用此接口。

```json
{
  "host": {
    "namespace": "metal3",
    "name": "worker-0",
    "bmcAddress": "redfish+https://<bmc-host>/redfish/v1/Systems/<id>",
    "bmcUsername": "admin",
    "bmcPassword": "由调用方安全提供",
    "bootMACAddress": "00:11:22:33:44:55",
    "inspectionDisabled": true
  },
  "status": {
    "operationalStatus": "detached",
    "errorMessage": "",
    "poweredOn": true,
    "errorCount": 0,
    "provisioning": {
      "state": "provisioned",
      "ID": "源 BMH status 中的值"
    }
  }
}
```

| 字段 | 中文含义 |
|---|---|
| `host` | 新集群中要创建的主机；子字段与普通登记接口一致，BMC 凭据会写入新 Secret |
| `status` | 从源 BareMetalHost 导出的完整 status，不是调用方自行编造的期望状态 |
| `status.provisioning.state` | 只接受 `available`、`ready`、`provisioned` 或 `externally provisioned` 稳定状态 |
| `status.operationalStatus` | 只接受 `OK` 或 `detached`；存在当前 BMO error 的状态会被拒绝 |

SDK 会在创建 BMH 的同一个 Kubernetes 请求中写入 `baremetalhost.metal3.io/status`，BMO 首次 reconcile 后会消费并删除它。若导入的 `operationalStatus` 是 `detached`，SDK 还会自动写入 `baremetalhost.metal3.io/detached`，防止目标 BMO 立即把主机登记到 Ironic；核对迁移结果后必须显式调用 `attach` 才恢复管理。普通 `annotations` 字段仍禁止直接写这些控制 annotation。即使旧集群导出的历史 status 中存在 `status.provisioning.firmware`，也必须删除该字段后再导入，因为没有 driver 支持。

## 安装系统字段

`POST .../actions/install` 和 `POST .../actions/reinstall`

| 字段 | 必填 | 中文含义 |
|---|---:|---|
| `imageURL` | 是 | 系统镜像地址，支持 HTTP(S)、`oci://` 和 live ISO |
| `checksum` | 视镜像而定 | 镜像校验值或校验文件 URL；普通 HTTP(S) 镜像应使用非 MD5 校验 |
| `checksumType` | 视镜像而定 | 校验算法，例如 `sha256`、`sha512` |
| `diskFormat` | 否 | 镜像格式，例如 `qcow2` 或 `raw` |
| `ociAuthSecretName` | 私有 OCI 镜像必填 | 同 namespace 已存在的 Docker registry Secret 名称；接口不接收 Secret 内容 |
| `rootDeviceHints` | 否 | 指定系统安装目标磁盘 |
| `userData` | 否 | cloud-init 或 Ignition 的首次启动配置 |
| `networkData` | 否 | config-drive 网络配置 |
| `metaData` | 否 | config-drive 主机元数据 |
| `powerOn` | 是 | 安装完成后是否要求开机 |
| `wait`、`timeoutSeconds` | 否 | 是否等待安装完成以及最长等待时间 |

`POST .../actions/custom-deploy` 的 `method` 是站点自定义部署步骤名，不接收 `imageURL`；其余数据和等待字段含义与安装接口一致。

## 电源、脱管与 BMC 更新字段

重启 `POST .../actions/reboot`：

| 字段 | 中文含义 |
|---|---|
| `mode` | 空值由 BMO 选择；`soft` 尝试操作系统软重启；`hard` 执行硬重启 |
| `force` | 是否强制重启；只在明确接受业务中断和数据损坏风险时使用 |
| `wait`、`timeoutSeconds` | 是否等待主机重新回到稳定状态 |

脱管 `detach` 的 `force:true` 表示正常条件不能满足时仍强制脱管；`attach` 重新交给 BMO/Ironic 管理。更新 BMC 的 `address`、`username`、`password` 分别是新 BMC 地址、用户名和密码，空字段表示不修改该项。

`POST .../actions/adopt-external` 接受通用 `wait`、`timeoutSeconds` 字段。它只允许没有待部署 `image`/`customDeploy` 的 `available` 或 `ready` 主机，表示操作系统已经由其他平台安装；成功后 BMO 只管理允许的电源、重启、servicing 和删除流程。该转换是单向的，SDK 不提供把 `externallyProvisioned` 改回 `false` 的接口。

### 两阶段重启

第一步调用 `POST .../actions/phased-reboot-start`：

```json
{"mode":"soft","force":false,"wait":true,"timeoutSeconds":300}
```

返回 Operation 的 `id` 就是本调用方唯一的 `phaseID`。`wait:true` 表示等待 BMO 将主机真正关机，但 Operation 仍保持 `Running`，因为还没有放行开机。

维护任务完成后调用 `POST .../actions/phased-reboot-complete`：

```json
{"phaseID":"第一步返回的 Operation ID","wait":true,"timeoutSeconds":600}
```

| 字段 | 中文含义 |
|---|---|
| `mode` | 与简单重启相同：空值自动选择，`soft` 优先软关机，`hard` 硬关机 |
| `force` | 是否强制执行关机 |
| `phaseID` | start 返回的 Operation ID；complete 只删除这个 ID 对应的 annotation |
| `wait` | start 时等待关机；complete 时等待全部客户端释放 hold 并恢复开机、`operationalStatus=OK` |

SDK 使用 `reboot.metal3.io/{phaseID}`。开始时主机必须期望在线且当前已经开机，以便“状态变为关机”能证明 BMO 已经执行第一阶段。多个客户端可以同时设置自己的 annotation；任何一个客户端都不能通过本接口删除其他客户端的 hold。如果 complete 时主机尚未关机，会返回状态错误，避免 annotation 在 BMO 执行前被过早删除。

## BIOS 设置与固件升级字段

`PATCH .../firmware-settings`：

| 字段 | 中文含义 |
|---|---|
| `settings` | BIOS 设置名称到目标值的映射；有效名称和值必须查询该主机的 FirmwareSchema，不能猜测 |
| `wait`、`timeoutSeconds` | 是否等待设置通过校验并应用完成 |

`PATCH .../firmware-components`：

| 字段 | 中文含义 |
|---|---|
| `updates[].component` | 固件部件，只允许 `bios`、`bmc` 或 `nic:<网卡名>` |
| `updates[].url` | Ironic 能访问的 HTTP(S) 固件镜像地址 |
| `wait`、`timeoutSeconds` | 是否等待升级完成；已部署主机应先提交更新，之后通过重启执行 |

已部署主机的顺序是：先创建同名 HostUpdatePolicy 并设为 `onReboot`；再以 `wait:false` 提交 settings/components；最后调用 `reboot` 且设 `wait:true`。

## 外部巡检数据字段

`POST .../actions/external-inspection`

```json
{
  "hardwareDetails": {
    "ramMebibytes": 32768,
    "cpu": {"arch": "x86_64", "model": "Example CPU", "count": 16},
    "nics": [{"name": "eno1", "mac": "<mac>", "ip": "<ip>", "vlanId": 100, "pxe": true}],
    "storage": [{"name": "/dev/disk/by-id/disk-0", "type": "SSD", "sizeBytes": 500000000000}]
  },
  "wait": true,
  "timeoutSeconds": 300
}
```

| 字段 | 中文含义 |
|---|---|
| `hardwareDetails.ramMebibytes` | 内存容量，单位 MiB |
| `hardwareDetails.cpu` | CPU 架构、型号、频率、核心/线程数量和 flags |
| `hardwareDetails.nics` | 网卡名称、MAC、IP、速率、VLAN、PXE 能力和 LLDP 信息 |
| `hardwareDetails.storage` | 磁盘稳定设备名、类型（`HDD`/`SSD`/`NVME`）、字节容量、序列号和 WWN |
| `hardwareDetails.systemVendor` | 服务器厂商、产品型号和序列号 |
| `hardwareDetails.firmware` | 检测到的 BIOS 厂商、版本和日期；这是只读硬件事实，不是已删除的 `spec.firmware` 配置接口 |
| `wait`、`timeoutSeconds` | 是否等待 BMO 消费并移除 hardware details annotation |

SDK 会校验负数容量、磁盘类型、MAC/IP 和 VLAN 范围。只有 inspection 已禁用，或主机尚无 HardwareData 时才允许提交；正在 inspecting 的主机会被拒绝。

## 其他 Metal3 资源字段

### BMCEventSubscription

| 字段 | 中文含义 |
|---|---|
| `hostName` | 要订阅事件的同 namespace BareMetalHost 名称 |
| `destination` | 接收 Redfish 事件的 HTTP(S) webhook 地址 |
| `context` | 随事件一起返回的调用方上下文字符串 |
| `httpHeadersRef` | 保存 webhook 自定义请求头的 Kubernetes Secret 引用 |

### DataImage

`url` 是 Ironic 能访问的附加数据镜像 HTTP(S) 地址；资源名通常与目标 BareMetalHost 同名。

### HostClaim

`spec` 是 BMO 原生结构：`poweredOn` 表示认领成功后是否开机；`image` 是要部署的镜像；`customDeploy` 是自定义部署方法，不能与 `image` 同时设置；`hostSelector.matchLabels` 用标签选择主机；`userData`、`networkData`、`metaData` 是配置 Secret 引用；`consumerRef` 表示谁在使用主机；`failureDomain` 是优先故障域。

### HostDeployPolicy

`spec.hostClaimNamespaces` 控制允许跨 namespace 认领的来源：`names` 是明确允许的 namespace 列表，`nameMatches` 是 namespace 名称正则表达式，`hasLabels` 要求 namespace 具有指定标签。

### HostUpdatePolicy

| 字段 | 可选值 | 中文含义 |
|---|---|---|
| `spec.firmwareSettings` | `onPreparing`、`onReboot` | 在 preparing 阶段或下一次重启时应用 BIOS 设置 |
| `spec.firmwareUpdates` | `onPreparing`、`onReboot` | 在 preparing 阶段或下一次重启时升级 BIOS/BMC/NIC 固件 |

## 返回字段

生命周期接口返回 Operation：`id` 是本次操作标识；`kind` 是操作类型；`host` 是目标主机；`phase` 是 `Pending`、`Running`、`Succeeded`、`Failed` 或 `Cancelled`；`startedAt`/`finishedAt` 是开始和结束时间；`message` 是状态或错误说明。当前 Operation 不跨 API 进程持久化，`wait:false` 后必须以 BareMetalHost 的实际 `status` 为最终依据。

请求体最大 1 MiB，未知 JSON 字段、多个 JSON 对象和非法状态转换都会被拒绝。BMC 密码和 cloud-init 数据只应通过 HTTPS 发送；服务不会记录请求体，也不会把 BMC 密码或登录 Cookie 返回给调用方。通用 Host PATCH 不接受 `baremetalhost.metal3.io/*`、`inspect.metal3.io*` 或 `reboot.metal3.io*` 控制 annotation，必须调用对应专用 action。

`wait:false` 的 lifecycle 调用只表示期望状态已提交。返回的 Operation 当前不跨进程持久化，调用方应继续查询 BMH；Minikube 清单只运行一个 API 副本。

维护请求示例：

`POST .../actions/configure-raid`：

```json
{
  "raid": {
    "hardwareRAIDVolumes": [{"level": "1", "sizeGibibytes": 100}],
    "softwareRAIDVolumes": null
  },
  "wait": true,
  "timeoutSeconds": 1800
}
```

`PATCH .../firmware-settings`：

```json
{
  "settings": {"BootMode": "Uefi", "ProcCores": 16},
  "wait": true,
  "timeoutSeconds": 1800
}
```

`PATCH .../firmware-components`：

```json
{
  "updates": [
    {"component": "bios", "url": "https://firmware.example/bios.bin"},
    {"component": "nic:eth0", "url": "https://firmware.example/nic.bin"}
  ],
  "wait": false
}
```

`BareMetalHost.spec.firmware` 和旧 `FirmwareConfig` 没有 driver 支持，SDK 与 HTTP API 均不暴露。可用的两条路径是 HostFirmwareSettings（BIOS 设置）和 HostFirmwareComponents（固件镜像）。对于 provisioned/externally provisioned 主机，先 `PUT` 同名 HostUpdatePolicy 为 `onReboot`，再以 `wait:false` 提交 settings/components，最后调用 `reboot` 且 `wait:true` 等待 servicing 完成。

安装 OCI 私有镜像时可提供 `ociAuthSecretName`；该 Secret 必须已经存在于 BMH namespace，类型只能是 `kubernetes.io/dockerconfigjson` 或 `kubernetes.io/dockercfg`。API 不接收也不回传 registry Secret 内容。

`inspection-mode` 的 `mode` 只能是 `automatic` 或 `disabled`。`preprovisioning-network-data` 接受 `{"networkData":"..."}`，空字符串表示清除引用。巡检、RAID、固件设置都由 BMO/Ironic 异步执行；`wait:true` 才等待状态收敛。
