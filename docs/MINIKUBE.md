# Minikube：Metal3 + Ironic

本文描述仓库自带的可选一体化部署清单。主要使用方式仍是：业务程序在集群外引入 Go SDK，使用 kubeconfig 连接 Kubernetes API。只有非 Go 调用方需要部署 `go-metal3-api`。

这套 profile 面向单节点开发、联调和小规模验证，不是高可用生产架构。它部署 BMO、Ironic 37、API 和 Ingress。Ironic 负责登记、巡检、清理和镜像部署。

```text
外部 Go 程序
    | kubeconfig
    v
Kubernetes API ----------------------> BMO / BareMetalHost

浏览器 -> HTTPS/WSS -> Ingress -> go-metal3-api
                                   | Kubernetes API
                                   +-> BMO
```

## 数据库边界

Ironic 不是“无数据库”服务。仓库的可选 Minikube profile 使用 Ironic 内置 SQLite，并把 `/data/db/ironic.sqlite` 放在 `baremetal-operator-system/ironic-data` PVC；Pod 重建不会丢失数据，但删除 PVC 或整个 profile 会删除数据库。某些现网环境可能使用独立 MariaDB + PVC，仓库部署脚本不会替换该数据库策略。

生产环境应使用受管 MariaDB、备份、Ironic API TLS 和多节点故障设计。

## 网络前置条件

运行脚本前准备：

- x86_64 Linux、Docker、Minikube、kubectl、OpenSSL；建议至少 4 CPU、8 GiB 内存、40 GiB 磁盘；
- `PUBLIC_HOST`：可选 HTTP API 的 DNS 名；
- `TLS_CERT_FILE`、`TLS_KEY_FILE`：覆盖该 DNS 名的证书和私钥；
- `PUBLIC_BIND_IP`：宿主机绑定 80/443 的地址，默认使用本机可访问地址；
- `IRONIC_IMAGE_BIND_IP` 和 `IRONIC_IMAGE_BASE_URL`：BMC/IPA 可访问的镜像服务 6180 地址；
- `IRONIC_CALLBACK_BIND_IP` 和 `IRONIC_CALLBACK_BASE_URL`：IPA 可回调的 Ironic API 6385 地址。

用户只通过 Kubernetes API 或 HTTPS Ingress 进入。Ironic 6385、镜像端口 6180 和 BMC 管理地址不应暴露到不可信网络。Minikube Docker driver 不应在业务网卡运行 DHCP；需要 PXE 时必须使用独立 provisioning bridge/VLAN。

## 部署

以下命令只应在用户指定的远程 Metal3 节点执行，不要在开发工作站启动 Minikube 或 Ironic。

```bash
export PUBLIC_HOST=metal3.example.com
export TLS_CERT_FILE=/secure/path/fullchain.pem
export TLS_KEY_FILE=/secure/path/privkey.pem
export PUBLIC_BIND_IP=<public-bind-ip>
export IRONIC_IMAGE_BIND_IP=<ironic-image-bind-ip>
export IRONIC_IMAGE_BASE_URL=http://<ironic-image-bind-ip>:6180
export IRONIC_CALLBACK_BIND_IP=<ironic-callback-bind-ip>
export IRONIC_CALLBACK_BASE_URL=http://<ironic-callback-bind-ip>:6385

./scripts/minikube-deploy.sh
./scripts/minikube-verify.sh
```

脚本使用独立 profile `go-metal3`，构建 `go-metal3-api:dev`，并生成 API key 与 Ironic Basic Auth。凭据保存在 `.artifacts/minikube/credentials`（权限 0600）和 Kubernetes Secret 中，脚本不把明文值写入日志。

## 验收边界

`minikube-verify.sh` 验证 Pod、PVC、HTTPS Ingress、API key discovery，以及 Ironic 6385 的 Basic Auth。真实主机登记、巡检、清理和镜像写盘仍需在用户指定的远程环境单独验收。
