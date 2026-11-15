# 外部隧道最终出口（开发版）

本分支基于 xx-ui v3.11.0，官方源码提交为 `c621ba9014a6aa21fd7c9ecf45655e44e24ebf13`。本地 `6c5c1f7` 是该源码归档的基线快照。功能独立实现，没有复制 LucX 的代码。

```text
原有客户端 → xx-ui / Xray 入站 → 路由选择外部出口标签
  → 本机认证 SOCKS gate → 官方客户端 → 远端隧道服务器 → 目标网站
```

## 支持范围

| 出口        | 固定客户端版本   | 隧道传输        | 业务流量    |
| ----------- | ---------------- | --------------- | ----------- |
| NaiveProxy  | v154.0.8037.49-5 | HTTPS / QUIC    | TCP CONNECT |
| mieru       | v3.39.0          | TCP             | TCP CONNECT |
| TrustTunnel | v1.1.11          | HTTP/2 / HTTP/3 | TCP CONNECT |

仅管理当前面板所在 Linux 主机的客户端。安装脚本支持 amd64、arm64；本次实际运行验证使用 amd64。这里的 QUIC/HTTP/3 是隧道传输方式，不代表支持客户端 UDP 流量。UDP ASSOCIATE 会被明确拒绝，QUIC 网站流量需由使用方允许回落到 TCP；本功能不替客户端改协议。

不更换 Xray 内核，不改变现有入站协议，不创建 TUN，不接管主机路由或防火墙，不向受管 node 分发客户端。远端的 NaiveProxy、mita 或 TrustTunnel endpoint 需要自行准备。

## 安装与使用

1. 在独立 Linux 测试实例构建、运行此分支。前端要求和 Go 版本以 `frontend/package.json`、`go.mod` 为准：

   ```bash
   npm --prefix frontend ci
   npm --prefix frontend run build
   go build -o x-ui-panel .
   ```

2. 显式安装所需客户端到 `${XUI_BIN_FOLDER}/external-proxies`。先确认自己的 `XUI_BIN_FOLDER`；未设置时，当前代码使用工作目录下的 `bin`。例如从项目根目录运行：

   ```bash
   bash scripts/install-external-clients.sh ./bin/external-proxies all
   # 也可把 all 换成 naive、mieru 或 trusttunnel。
   ```

   也可以在网页「出站 → 外部隧道出口 → 客户端程序管理」中查看当前版本、检查官方稳定版并安装或更新。Web 操作仅供管理员会话或完整 admin API token 使用；monitor、node-sync、node-admin token 无权安装客户端。后台下载会校验官方 SHA-256、ELF 架构和版本，原子替换后只重启本面板管理的对应协议客户端，保留 SOCKS gate；相关连接可能断开。启动失败时尝试恢复旧程序，错误会显示在页面中。独立于本管理器运行的其他服务不受它控制。

   Web 下载沿用面板网络出站，环境变量 `XUI_EXTERNAL_CLIENT_PROXY` 可单独覆盖这些请求的代理。仅允许固定官方仓库及 HTTPS 发布资产，不接受任意下载 URL 或文件上传。一次只允许一个客户端操作。安装包上限 64 MiB，展开上限 128 MiB；官方发布缺少 SHA-256 摘要时拒绝安装。NaiveProxy 解压需要 `xz`，缺失时报告错误而不替换旧文件。

   Linux 构建包仍附带 `scripts/install-external-clients.sh`，从源码手动部署时也应复制该脚本。面板在 `${XUI_BIN_FOLDER}/external-proxies` 中按固定文件名 `naive`、`mieru`、`trusttunnel` 查找客户端。

   脚本只从固定的官方 release 下载，逐一核对硬编码 SHA-256，再原子替换目标程序。需要 `curl`、`tar`、`sha256sum`、`xz`；面板进程需要执行这些程序的权限。保存网页表单不会触发下载。更新客户端后，停用并重新启用对应出口以替换正在运行的进程。

3. 登录面板，在左侧 **出站** 页底部的 **外部隧道出口** 添加出口。填写协议、唯一标签、服务器、端口、用户名、密码；NaiveProxy/TrustTunnel 可填写 TLS 服务器名称。NaiveProxy 要覆盖 TLS 名称时，服务器地址需填写 IP。需要可验证的服务器证书；首版表单没有“不验证证书”或自定义 CA 选项。

4. 保存后，在原有路由界面选择该标签。保存外部出口立即写入数据库并在 Xray 已运行时应用，路由编辑仍遵循原有保存流程。存在未保存的 Xray 修改时，外部出口编辑会暂时禁用，避免刷新覆盖草稿。

   路由规则示例（把 `entry` 换成实际入站标签）：

   ```json
   { "type": "field", "inboundTag": ["entry"], "outboundTag": "naive-exit" }
   ```

   如要所有现有入站都走该出口，应明确为它们添加规则并检查规则顺序。仅创建出口不会自动改变全局默认出站。

5. 点击“测试 HTTPS”验证该出口到固定 HTTPS 探测地址的连通性。**客户端运行中**只表示官方进程已拥有 SOCKS 监听端口，不表示远端已连接；探测失败不会回退到 direct。

如果提示“配置已保存，但客户端/Xray 应用失败”，数据库写入已经完成。关闭编辑框并刷新列表后编辑已有条目，避免重复新建。Linux 开发包只包含面板程序和外部客户端安装脚本，需要另行具备 Xray、GeoData 等原有运行环境；面板二进制动态链接 glibc，其他发行版兼容性尚未验证。

## 故障、凭据与删除行为

- 缺少二进制、启动失败、进程退出、禁用出口时，选中该标签的连接失败。禁用后保留 blackhole 标签并关闭现有受管连接，避免路由误用默认出站。
- TrustTunnel 强制 `killswitch_enabled = true`。它的官方客户端对字面回环目标另有本机旁路，因此 gate 还拒绝回环 IP、未指定地址和 `localhost` / `.localhost` 名称。普通域名保持为 SOCKS 域名交给客户端；未添加本地 DNS 预解析。
- Xray 交给 gate 的目标如果已是 IP，就保留该 IP；如果是域名，就保留域名。Xray 自身的 DNS、嗅探和 `domainStrategy` 仍由现有配置控制。本功能没有保证所有主机 DNS 都走隧道。
- 程序使用独立子进程、退避重试、Linux `/proc` 端口归属检查；在已连接 TCP 上再次核对归属和进程代际，随后在同一连接握手。
- 远端密码写入数据库，但不会出现在 list/save 响应中。编辑时留空保留旧密码。配置目录权限为 `0700`，配置文件为 `0600`；数据库备份及私有配置仍属于敏感文件，需要按凭据管理。日志有限长，并脱敏原始、URL、Basic、JSON 和 TOML 转义形式；这不是对任意第三方输出的绝对保密承诺。
- 标签创建后不可改名。与模板、订阅、API、metrics、reverse handler 等标签冲突时拒绝保存；订阅刷新发生冲突时保留旧快照。`amneziawg-v6-` 前缀留给既有自动生成逻辑。
- 路由、balancer 前缀选择器、入站设置、node 或面板请求仍引用出口时，拒绝删除。删除与 node/panel/MTProto 引用写入共用锁，并拒绝保存已经不存在的目标。

## 开发验证

普通检查：

```bash
make dist-stub
go test -shuffle=on -count=1 ./...
go test -race ./internal/externalproxy ./internal/web/service ./internal/web/controller -run 'TestExternal|TestManaged|TestBackend|TestTrustTunnel|TestClientLog|TestProtocol'
npm --prefix frontend run typecheck
npm --prefix frontend run lint
npm --prefix frontend run format:check
npm --prefix frontend run test -- --project=unit --project=components
npm --prefix frontend run build
npm --prefix frontend run build-storybook
git diff --check
```

可选真实协议 fixture（程序需自行从官方 release 下载、核验）：

```bash
EXTERNAL_CLIENT_BIN_DIR=/absolute/path/to/clients \
EXTERNAL_MITA_BINARY=/absolute/path/to/mita \
XRAY_E2E_BINARY=/absolute/path/to/xray \
EXTERNAL_TRUST_ENDPOINT_BINARY=/absolute/path/to/trusttunnel_endpoint \
go test -count=1 -v ./internal/externalproxy \
  -run 'TestOfficialClientConfigStartup|TestRealXrayMieru|TestOfficialTrustTunnelDisconnect'
```

mieru fixture 使用独立临时目录、非特权服务端和官方 Xray；验证 HTTPS 成功及禁用后的失败。TrustTunnel fixture 使用临时自签 CA，通过测试配置单独固定该证书，保持验证开启，不修改系统证书库；验证 HTTPS 成功后杀死 endpoint，新连接失败且目标请求计数不增长。fixture 额外证书固定不等于网页支持自定义 CA。正式验证结果和未验证项见交付的验证报告。

## 迁移、回退与发布边界

独立测试数据库已执行新增表初始化；没有连接用户生产数据库。正式升级前备份数据库和配置。回退前移除对外部标签的引用，停用出口，再停止此分支面板，使用升级前备份恢复旧版；不要让旧版在带有外部标签路由的配置上继续运行。

项目现名为 xx-ui，源码仓库为 `a923946240/xx-ui`。面板和管理脚本的更新来源已指向该仓库。当前通过源码构建管理版本，发行包及私库更新认证尚未配置。原版安装器与部署模板保留为上游参考。

本地 Windows Defender 隔离了未修改的上游 `install.sh`，当前源码目录缺少该文件。没有恢复、添加排除项或关闭防护，也没有将这次隔离作为功能改动提交。此开发版提供独立的外部客户端安装脚本，不提供可直接替换上游安装流程的完整发布包。

## 实现依据

- [NaiveProxy 固定版本 USAGE](https://github.com/klzgrad/naiveproxy/blob/v154.0.8037.49-5/USAGE.txt)
- [mieru v3.39.0](https://github.com/enfein/mieru/tree/v3.39.0)，`pkg/cli/client.go`、`pkg/appctl/client.go` 与客户端配置定义。
- [TrustTunnelClient v1.1.11 配置](https://github.com/TrustTunnel/TrustTunnelClient/blob/v1.1.11/trusttunnel/README.md)
- [TrustTunnel 断线状态机](https://github.com/TrustTunnel/TrustTunnelClient/blob/v1.1.11/core/src/vpn_fsm.cpp#L104)
- [TrustTunnel 回环地址旁路](https://github.com/TrustTunnel/TrustTunnelClient/blob/v1.1.11/core/src/tunnel.cpp#L1571)
- [TrustTunnel v1.1.0 服务端 fixture 配置](https://github.com/TrustTunnel/TrustTunnel/blob/v1.1.0/CONFIGURATION.md)

二进制下载 URL 与各架构 SHA-256 的完整清单位于 `scripts/install-external-clients.sh`。
