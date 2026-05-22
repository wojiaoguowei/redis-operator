# RedisFailover 基线合入 RedisCluster 能力改造方案

## 1. 背景

当前环境已经在使用 `spotahome/redis-operator` 提供的 `RedisFailover` 能力，但该基线不支持 Redis Cluster。现有 `redis-operator` 仓库已经具备 `RedisCluster` 的控制面能力，并且补充了本地存储相关逻辑，包括：

- local PV 创建与回收
- `GeneratePath` 自定义资源创建与清理
- cluster leader/follower 双 StatefulSet 的 PV 处理
- node-conf PV 与 data PV 的同节点约束

目标不是简单“并排部署两个独立产品”，而是把当前 `RedisCluster` 能力合入 `redisfailover` 基线仓库，形成一个统一产品入口；但运行时仍保持隔离，避免两套控制逻辑深度耦合。

## 2. 目标

目标形态如下：

- 一个代码仓库，以 `redisfailover` 基线为主仓库
- 一个交付入口，例如单一 Helm umbrella chart
- 两套 CRD 并存
  - `RedisFailover`
  - `RedisCluster`
- 两个 operator 运行时并存
  - failover operator：只处理 `RedisFailover`
  - cluster operator：只处理 `RedisCluster`
- 对用户呈现为一个 Redis Operator 产品

## 3. 不建议的方案

不建议把两套 operator 逻辑硬合并到同一个 controller manager 进程中，原因如下：

- `RedisFailover` 与 `RedisCluster` 的资源模型不同
- 当前 cluster 代码已经包含较完整的调谐、扩缩容、本地 PV、清理逻辑，强行改写成本高
- 两套控制器的升级节奏、排障入口、RBAC 和 leader election 很容易互相影响
- 后续继续跟进 `spotahome` 基线升级时，深度改写会显著增加维护成本

因此推荐“产品层合并，运行时隔离”。

## 4. 目标架构

### 4.1 仓库层

以 `redisfailover` 基线仓库为主仓库，增加 cluster operator 相关代码、镜像构建和安装清单。

建议目录形态：

```text
repo/
  operators/
    redisfailover/
    rediscluster/
  charts/
    redis-platform/
  docs/
```

如果不希望大改目录，也可以直接把 cluster 代码合入现有仓库结构，但需要保证逻辑边界清晰：

- `RedisFailover` 代码路径保持原状
- `RedisCluster` 代码集中放在独立 package/module
- chart 和 RBAC 分开维护，最终由 umbrella chart 汇总

### 4.2 运行时层

部署两个 Deployment：

- `redis-failover-operator`
- `redis-cluster-operator`

它们必须分别拥有：

- 独立的 `LeaderElectionID`
- 独立的 metrics 端口
- 独立的 webhook 名称或按需关闭 webhook
- 独立的 ServiceAccount / Role / ClusterRole

### 4.3 使用层

用户按拓扑类型选择 CR：

- 高可用主从 + sentinel：使用 `RedisFailover`
- Redis Cluster 分片：使用 `RedisCluster`

不做两类 CR 的自动互转，也不尝试抽象成一个统一 CR。

## 5. 需要迁移的文件

以下文件是从当前仓库迁移 `RedisCluster` 能力到 `redisfailover` 基线时的建议清单。

### 5.1 必须迁移

API / CRD：

- `api/common/v1beta2/common_types.go`
- `api/common/v1beta2/doc.go`
- `api/common/v1beta2/zz_generated.deepcopy.go`
- `api/rediscluster/v1beta2/groupversion_info.go`
- `api/rediscluster/v1beta2/rediscluster_types.go`
- `api/rediscluster/v1beta2/rediscluster_default.go`
- `api/rediscluster/v1beta2/rediscluster_conversion.go`
- `api/rediscluster/v1beta2/zz_generated.deepcopy.go`
- `config/crd/bases/redis.redis.opstreelabs.in_redisclusters.yaml`

Controller：

- `internal/controller/rediscluster/rediscluster_controller.go`

Cluster 运行时主逻辑：

- `internal/k8sutils/redis-cluster.go`
- `internal/k8sutils/redis.go`
- `internal/k8sutils/cluster-scaling.go`
- `internal/k8sutils/statefulset.go`
- `internal/k8sutils/services.go`
- `internal/k8sutils/poddisruption.go`
- `internal/k8sutils/finalizer.go`
- `internal/k8sutils/labels.go`
- `internal/k8sutils/const.go`
- `internal/k8sutils/kube.go`

共享依赖：

- `internal/controller/common/status.go`
- `internal/controller/common/finalizer.go`
- `internal/controller/common/labels.go`
- `internal/controller/common/redis/check.go`
- `internal/controller/common/redis/heal.go`
- `internal/controllerutil/controller_common.go`
- `internal/service/redis/client.go`

接入点：

- `internal/cmd/manager/cmd.go`
- `internal/controller/common/scheme/scheme.go`

### 5.2 建议一起迁移

本地存储与 PVC：

- `internal/k8sutils/localpv.go`
- `internal/k8sutils/pvc.go`
- `internal/k8sutils/client.go`

环境与特性开关：

- `internal/envs/envs.go`
- `internal/features/features.go`

监控：

- `internal/monitoring/rediscluster.go`
- `internal/monitoring/main.go`

Webhook：

- `api/rediscluster/v1beta2/rediscluster_webhook.go`
- `internal/webhook/pod_webhook.go`

RBAC 参考：

- `config/rbac/role.yaml`
- `api/rbac.go`

### 5.3 建议迁移的测试

- `internal/controller/rediscluster/rediscluster_controller_test.go`
- `internal/k8sutils/redis-cluster_test.go`
- `internal/k8sutils/statefulset_test.go`
- `internal/k8sutils/finalizer_test.go`
- `internal/k8sutils/localpv_test.go`
- `internal/k8sutils/pvc_test.go`
- `internal/k8sutils/services_test.go`
- `internal/k8sutils/poddisruption_test.go`

### 5.4 不应迁移

以下是 standalone / replication / sentinel 路径，不属于目标范围：

- `api/redis/v1beta2/*`
- `api/redisreplication/v1beta2/*`
- `api/redissentinel/v1beta2/*`
- `internal/controller/redis/*`
- `internal/controller/redisreplication/*`
- `internal/controller/redissentinel/*`
- `internal/k8sutils/redis-standalone.go`
- `internal/k8sutils/redis-replication.go`
- `internal/k8sutils/redis-sentinel.go`

## 6. 关键实现原则

### 6.1 Cluster 只保留 cluster 能力

迁入 `redisfailover` 基线后，cluster operator 只负责：

- `RedisCluster` CR
- cluster 扩缩容与恢复
- cluster service / sts / pdb / config 生成
- local PV 与 `GeneratePath`

不要再把 standalone、replication、sentinel 路径一起带进目标仓库。

### 6.2 本地存储能力必须整体迁入

如果生产环境依赖 local PV，则以下能力必须成套迁入：

- local PV 创建逻辑
- `GeneratePath` 创建逻辑
- PV 存在性判断逻辑
- cluster leader / follower 各自的 PV 逻辑
- node-conf 与 data PV 的同节点约束
- finalizer 中与 PV 同生命周期的 `GeneratePath` 删除逻辑

否则会出现“PV 能创建但目录没预生成”或者“CR 删除后残留 `GeneratePath`”的问题。

### 6.3 PV 存在性判断不能依赖 CR 名称标签

当前规则已经调整为按“预期 PV 名称”判断，而不是按 CR name label 过滤。这个行为必须保留，因为：

- 不同 namespace 下可能有同名 CR
- 预置 PV 可能根本没有 operator 标签

### 6.4 node-conf PV 和 data PV 要求同节点

在 cluster 本地存储场景下，同一 Pod 的：

- node-conf PV
- data PV

需要落到同一个 node。当前实现通过复用每个 replica 已选择的 data PV 节点来完成，这部分不要在迁移时拆散。

## 7. 详细实施步骤

### 阶段 0：基线对齐与 PoC

目标：

- 明确 `redisfailover` 基线版本
- 确认使用方式是 subtree、vendor 还是直接代码合并
- 验证两个 operator 在同集群可并存

实施步骤：

1. 拉取 `redisfailover` 基线最新稳定分支。
2. 盘点其当前目录结构、构建方式、CI、镜像发布、Helm 安装方式。
3. 做一个最小 PoC：
   - 原样部署 failover operator
   - 原样部署当前 cluster operator
   - 验证两者 CRD、RBAC、leader election、metrics 是否冲突
4. 形成兼容性清单：
   - 依赖版本冲突
   - controller-runtime 差异
   - CRD 安装策略差异

交付物：

- 一份 PoC 记录
- 一份差异清单

### 阶段 1：迁入 Cluster API 与控制器骨架

目标：

- 在 `redisfailover` 基线仓库中注册 `RedisCluster`
- 能启动 cluster controller，但先不要求功能完整

实施步骤：

1. 迁移 `api/common/v1beta2` 和 `api/rediscluster/v1beta2`。
2. 补 `scheme` 注册。
3. 补 manager/controller 注册入口。
4. 补 `RedisCluster` CRD 安装清单。
5. 补最小 RBAC，使 controller 可以启动。
6. 启动后验证：
   - operator 能正常启动
   - `RedisCluster` 能被 watch
   - reconcile 能进入但允许暂时失败

交付物：

- 新仓库中可编译、可启动的 `RedisCluster` controller 骨架

### 阶段 2：迁入 Cluster 主功能

目标：

- `RedisCluster` 的 StatefulSet、Service、PDB、配置生成能力完整可用

实施步骤：

1. 迁移 `redis-cluster.go`、`cluster-scaling.go`、`statefulset.go`、`services.go`、`poddisruption.go` 等核心逻辑。
2. 迁移依赖的共用工具类与 redis client。
3. 对照 `redisfailover` 基线的日志、事件、错误处理风格做必要适配。
4. 补单元测试与基础集成测试。
5. 验证以下主流程：
   - 创建 cluster
   - 删除 cluster
   - 扩容
   - 缩容
   - 故障恢复

交付物：

- 无 local PV 依赖的 `RedisCluster` 主流程可用

### 阶段 3：迁入 Local PV 与 GeneratePath 能力

目标：

- 在新基线中恢复当前 cluster 的本地存储完整行为

实施步骤：

1. 迁移 `localpv.go`、`pvc.go`、`client.go`。
2. 迁移 `GeneratePath` 创建逻辑：
   - local PV 创建前创建 `GeneratePath`
   - `GeneratePath` 中写入目录、权限、node affinity
3. 迁移 cluster 双 StatefulSet 的 PV 行为：
   - leader 需要独立处理
   - follower 需要独立处理
4. 迁移两类 PV 逻辑：
   - `node-conf` PV
   - `data` PV
5. 保留以下规则：
   - 持久化 PV 仅在开启 persistence 时创建
   - node-conf PV 仅在未预建、未配置 SC 且无默认 SC 的场景下创建
   - node-conf 无独立配置时使用固定默认值
6. 迁移并验证同 Pod 的 node-conf PV 与 data PV 同节点约束。
7. 迁移 finalizer 中的 PV / `GeneratePath` 联动删除逻辑。
8. 补 `generatepaths` RBAC。

交付物：

- local PV 模式下 cluster 可完整创建、绑定、删除、清理

### 阶段 4：统一安装与交付

目标：

- 对外形成一套安装入口，而不是两个散装 chart

实施步骤：

1. 新建 umbrella chart，例如 `redis-platform`。
2. 通过 values 控制两个 operator 的启停：
   - `failover.enabled`
   - `cluster.enabled`
3. 聚合 CRD、RBAC、Deployment、ServiceMonitor 等安装对象。
4. 为两个 operator 配置独立的：
   - leader election id
   - metrics port
   - webhook service name
   - service account
5. 编写安装和升级文档。

交付物：

- 一套统一 Helm 安装入口

### 阶段 5：回归测试与上线准备

目标：

- 确保并存场景稳定、可升级、可排障

实施步骤：

1. 做 failover 回归：
   - 创建
   - 主从切换
   - 删除
2. 做 cluster 回归：
   - 创建
   - 扩缩容
   - 本地存储
   - 删除
3. 做并存验证：
   - 同 namespace 并存
   - 不同 namespace 并存
   - 同时大量 reconcile
4. 验证升级路径：
   - 仅升级 failover operator
   - 仅升级 cluster operator
   - 一起升级 umbrella chart
5. 补充运行文档：
   - 问题定位入口
   - 资源归属关系
   - 回滚步骤

交付物：

- 上线前回归报告
- 升级与回滚说明

## 8. 验收标准

至少满足以下验收条件：

- `RedisFailover` 现有能力不回退
- `RedisCluster` 可在目标仓库中独立工作
- 两类 CRD 可同时安装并同时运行
- local PV 场景下 `GeneratePath` 能按 PV 生命周期创建和删除
- 同一 cluster 的 leader/follower StatefulSet 都能按预期创建 PV
- 同一 Pod 的 node-conf PV 与 data PV 在本地存储场景下位于同一节点
- 删除 CR 且 `keepAfterDelete=false` 时，PVC / PV / `GeneratePath` 清理符合预期
- 无标签的预置 PV 不会被“CR 名称过滤”逻辑误判

## 9. 风险与难点

### 9.1 依赖版本差异

`redisfailover` 基线与当前 cluster 代码可能使用不同版本的：

- `controller-runtime`
- `k8s.io/*`
- `client-go`

这会导致代码并入后需要补一轮兼容性修改。

### 9.2 安装清单冲突

需要重点检查：

- CRD 安装顺序
- RBAC 重名
- metrics 端口冲突
- leader election lease 冲突
- webhook 证书与 service 冲突

### 9.3 运行时排障复杂度

用户看到的是一个产品，但底层是两个 operator，排障文档必须明确：

- 哪类 CR 归哪个 operator 管
- 哪类日志在哪个 deployment 看
- 哪类资源由哪个 operator 创建

## 10. 工作量评估（无 AI）

以下评估基于：

- 1 名熟悉 Go / Kubernetes operator 的后端工程师
- 1 名提供联调与部署支持的平台/SRE 工程师（按部分时间投入）

### 10.1 推荐方案：产品层合并，运行时隔离

预计总工作量：`20 - 33 人天`

细分如下：

- 基线分析与 PoC：`2 - 4 人天`
- API / controller 接入：`4 - 6 人天`
- cluster 主逻辑迁移：`5 - 8 人天`
- local PV / GeneratePath 迁移：`4 - 6 人天`
- umbrella chart / CI / RBAC / 文档：`3 - 5 人天`
- 回归测试与修复：`2 - 4 人天`

换算周期：

- 1 人主导开发：约 `4 - 6 周`
- 1 后端 + 1 平台协同：约 `3 - 4 周`

### 10.2 不推荐方案：单进程深度合并

如果坚持把 failover 与 cluster reconcile 深度揉到一个 operator 进程内：

- 预计总工作量：`40 - 60 人天`
- 周期：`8 - 12 周`

主要增加项：

- 统一控制器框架与启动流程
- 更复杂的依赖兼容
- 更高的回归范围
- 后续升级成本明显增加

## 11. 推荐结论

推荐实施路径是：

1. 以 `redisfailover` 作为主仓库基线。
2. 把当前仓库中的 `RedisCluster` 能力按模块迁入。
3. 对外统一为一个产品与安装入口。
4. 对内保持两个 operator 运行时隔离。
5. local PV 与 `GeneratePath` 逻辑整体保留，不做功能裁剪。

这样做的优点是：

- 兼顾统一交付与低风险实施
- 不破坏现有 `RedisFailover` 生产使用方式
- 可以完整保留当前 cluster 和本地存储能力
- 后续对 failover 与 cluster 分别升级时更容易控制影响面
