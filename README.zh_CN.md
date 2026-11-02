# xx-ui

[English](README.md) · 简体中文

xx-ui 是基于 3x-ui v3.11.0 开发的 Xray 管理面板，增加了三种外部隧道出口、按路由规则自动切换主备出口的功能，以及炭黑磨砂主题。[项目仓库](https://github.com/a923946240/xx-ui)

## 通过外部隧道访问网站

客户端仍连接原有的 Xray 入站。在「出站」页面添加 NaiveProxy、mieru 或 TrustTunnel 出口并设置标签，再到路由规则中选择该标签。匹配规则的 TCP 连接会沿以下路径访问目标网站。

```text
客户端 → Xray 入站 → 规则指定的出站 → 本机隧道客户端 → 远端隧道服务器 → 目标网站
```

在面板所在的 Linux 主机上安装对应的隧道客户端，并准备好远端服务器的连接信息。三种外部出口均支持 TCP CONNECT。[安装步骤与支持的传输方式](docs/external-outbounds.md) · [外部出口 API](docs/content/docs/en/reference/api/external-outbounds.mdx)

## 为一条规则设置备用出口

例如，在「路由 → 路由规则」中编辑匹配 `domain:google.com` 的规则，将「出站标签」设为 A，「备用出站（自动切换）」设为 B。确认规则后，再点页面顶部的「保存」。正常时 Google 的新连接走 A，A 探测失败后新连接走 B，A 恢复并通过探测后，新连接自动切回 A。已有连接需要重连才能换出口。

默认每 15 秒通过 A 访问一次 `https://www.google.com/generate_204`。如果 Xray 模板已经设置了普通观测器或 burst 观测器，会沿用已有的探测参数。[出站与路由说明](docs/content/docs/zh/operations/outbounds-routing.mdx)

## 界面主题

暗色主题采用炭黑底色、半透明磨砂面板和背景粒子。系统开启「减少动态效果」时，面板会关闭粒子动画。

## 在 Linux 上从源码构建

在仓库根目录执行以下命令。构建需要 Go 1.27.3、Node.js 26、npm 11 或更高版本，以及供 SQLite 使用的 C 编译器。[Go 版本](go.mod) · [前端工具版本](frontend/package.json)

```sh
npm --prefix frontend ci
npm --prefix frontend run build
go build -o xx-ui .
```

生成的 `xx-ui` 可执行文件内含前端页面。要运行代理，请将 Xray 可执行文件及路由规则所需的地理数据文件放在 `bin/` 或 `XUI_BIN_FOLDER` 指定的目录下。Linux amd64 的核心文件名为 `xray-linux-amd64`。按需安装外部隧道客户端后，执行 `./xx-ui` 启动面板。[外部客户端安装脚本](scripts/install-external-clients.sh)

当前提供源码构建，原版安装器与部署示例保留为上游参考。私有仓库的发行包下载需要认证，面板的自动更新尚未配置这项认证。

## 上游与许可证

感谢 **MHSanaei 和 3x-ui 的所有贡献者**提供面板底座，感谢 XTLS 项目提供 Xray-core。本分支沿用 GNU 通用公共许可证第 3 版（GPLv3）。[上游 3x-ui v3.11.0](https://github.com/MHSanaei/3x-ui/tree/v3.11.0) · [Xray-core](https://github.com/XTLS/Xray-core) · [许可证全文](LICENSE)
