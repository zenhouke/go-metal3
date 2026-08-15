# go-metal3

`go-metal3` 是一个通过 Kubernetes API 操作 Metal3/BMO 的 Go SDK。它把声明式 spec/status 封装成 Host、Power、Provisioning、Maintenance、Resources 和集群能力探测 API。主要运行方式是让业务程序在集群外引入本 SDK，并向 `metal3sdk.New` 传入目标集群的 `*rest.Config`；SDK 不要求在 Kubernetes 中部署本项目的 HTTP API 容器。

当前实现包含：

- Kubernetes/Metal3 API discovery 与 BMO v0.13 全部 11 个 CRD 的能力报告；
- 裸金属登记、跨 BMO 集群状态重建导入、巡检后接管外部已部署主机、查询、筛选、元数据更新和两种显式删除语义；
- 显式 detach/attach，以及遵循该流程的 BMC 地址更新与凭据轮换；
- 开机、关机，以及由 BMO reboot annotation 驱动的 soft/hard/force 简单重启和两阶段重启；
- HTTP(S)、OCI（含 registry Secret）和 live-ISO 镜像校验，安装、customDeploy、同 URL 重装与卸载；
- userData、networkData、metaData 内容寻址不可变 Secret；
- `spec.inspectionMode` 巡检开关/重巡检、外部巡检数据、RAID、HostFirmwareSettings、HostFirmwareComponents 和预部署网络数据；
- BMCEventSubscription、DataImage、HostClaim、HostDeployPolicy、HostUpdatePolicy typed CRUD；
- cloud-init、Ignition v3 和 OpenStack network data 构造/校验；
- 带 TLS/Bearer/namespace allowlist 边界的 JSON API server；
- Minikube 中 BMO + Ironic 37 + Ingress 的可复现部署脚本；
- 状态等待、结构化错误、冲突重试、最小权限 RBAC 和 fake-client 单元测试。

实现状态和仍需具体部署提供的边界见 [`docs/STATUS.md`](docs/STATUS.md)，可选 HTTP 适配层及请求字段中文说明见 [`docs/HTTP_API.md`](docs/HTTP_API.md)，Minikube 部署见 [`docs/MINIKUBE.md`](docs/MINIKUBE.md)，设计依据见 [`docs/IMPLEMENTATION_PLAN.md`](docs/IMPLEMENTATION_PLAN.md)。

## 快速使用

外部程序只需要一个能访问 Kubernetes API 的 kubeconfig，填写目标集群对应的 `server` 与 CA/认证信息；不需要把 `go-metal3-api`、Ingress 或公网域名部署到集群。

```go
package main

import (
	"context"
	"log"
	metal3sdk "github.com/zenhouke/go-metal3"
	sdkclient "github.com/zenhouke/go-metal3/pkg/client"
	"k8s.io/apimachinery/pkg/types"
)

func main() {
	cfg, err := sdkclient.LoadConfig(sdkclient.ConfigOptions{Kubeconfig: "/secure/metal3.kubeconfig"})
	if err != nil {
		log.Fatal(err)
	}
	sdk, err := metal3sdk.New(metal3sdk.Options{Config: cfg})
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	info, err := sdk.Cluster().Info(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if !info.BareMetalHosts.Available {
		log.Fatal("cluster does not serve metal3.io/v1alpha1 BareMetalHost")
	}

	_, err = sdk.Hosts().Get(ctx, types.NamespacedName{Namespace: "metal3", Name: "worker-01"})
	if err != nil {
		log.Fatal(err)
	}
}
```

安装镜像时，HTTP(S) 镜像默认要求非 MD5 checksum；OCI 镜像使用 `oci://` URL；`live-iso` 不接受 checksum、root device hints 或 config-drive 数据。`Wait=false` 表示只提交 BMH 期望状态，调用方可稍后查询 BMH 或使用等待 API。

```go
op, err := sdk.Provisioning().Install(ctx, key, metal3sdk.InstallRequest{
	ImageURL:     "https://images.example/ubuntu.qcow2",
	Checksum:     "https://images.example/SHA256SUMS",
	ChecksumType: metal3v1alpha1.SHA256,
	UserData:     cloudConfig,
	NetworkData:  networkData,
	PowerOn:      true,
	Wait:         true,
	Timeout:      90 * time.Minute,
})
```

## 重要语义

- Kubernetes patch 成功只代表操作已提交；物理操作完成以 BMH `status` 收敛为准。
- 同镜像 URL 重装会先清空 provisioning 字段并等待主机回到 `available`，然后再提交新 image/config。
- `DeleteInventoryOnly` 会先等待 host 成为 `detached`，避免触发擦盘/清理；删除模式必须显式指定。
- BMC 地址更新仅在 registering、detached 或稳定状态执行；SDK 自己添加的 detached annotation 才会由 SDK 移除。
- 通用 Host 更新拒绝 Metal3 control annotations；重启、巡检、detach 等必须走专用方法。
- 跨集群迁移必须先在源集群 detach；目标集群使用 `Hosts().Import` 在创建 BMH 时原子提交状态重建 annotation。导入 detached status 时 SDK 会同时保留 detached annotation，直到调用 `Attach`，不能先 Add 再 Patch。
- 已登记并完成巡检的 available 主机可用 `AdoptExternallyProvisioned` 单向转为 externally provisioned；BMO 不支持再改回 false。
- 已删除无人支持的 `BareMetalHost.spec.firmware`/`FirmwareConfig` API。BIOS 设置使用 `HostFirmwareSettings`，BIOS/BMC/NIC 镜像升级使用 `HostFirmwareComponents`。
- provisioned 主机的 live update 流程是：应用同名 `HostUpdatePolicy(onReboot)`、以 `Wait=false` 提交 settings/components、最后 `Reboot(Wait=true)` 等待 servicing 回到 `OK`。
- 两阶段重启先调用 `StartPhasedReboot`，用返回的 Operation ID 等待并标识本客户端的关机 hold；完成维护后只把该 ID 传给 `CompletePhasedReboot`，不会移除其他客户端的 hold。

## 开发

```bash
go mod tidy
go test ./...
go test -race ./...
go vet ./...
```

依赖基线对齐 BMO 及 Metal3 API module `v0.13.2`、Kubernetes `v0.35.6` 与 controller-runtime `v0.23.3`。运行前仍应通过 `sdk.Cluster().Info` 和目标集群实际 CRD/BMO 版本确认兼容性。
