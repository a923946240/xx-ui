# NAT 出站地址同步

业务流量继续沿前置出站的 `Dialer Proxy` 转发。地址消息走已有 SSH 服务，前置只使用本机的 xx-ui 管理 API。

## 香港

为同步建立专用非 root 用户。把 `nat-outbound-sync.py` 安装到该用户可执行、不可修改的路径，例如 `/usr/local/libexec/nat-outbound-sync.py`。建立专用用户可写的私有状态目录，例如 `/var/lib/nat-outbound-sync`，权限设为 0700。给台湾和前置各生成一把独立的 SSH 密钥。在专用用户的 `authorized_keys` 放入以下两条授权，以各自的完整公钥替换占位符。

```text
restrict,command="/usr/bin/python3 /usr/local/libexec/nat-outbound-sync.py report /var/lib/nat-outbound-sync/taiwan.json" 台湾公钥
restrict,command="/usr/bin/python3 /usr/local/libexec/nat-outbound-sync.py read /var/lib/nat-outbound-sync/taiwan.json --max-age 60" 前置公钥
```

上报命令取香港 SSH 服务观察到的对端 IPv4。台湾到香港的 SSH 要从代理端口对应的公网出口发起。如果多 WAN 或 NAT 映射使用另一公网地址，先核对这两处地址是否一致。

## 台湾

在定时任务运行账号的 SSH 配置中建立 `hk-nat-report` 别名，指定香港的地址、专用用户、上报密钥和 `IdentitiesOnly yes`。核验并保存香港主机指纹后，先手动运行：

```sh
ssh -4 -n -o BatchMode=yes -o StrictHostKeyChecking=yes -o ConnectTimeout=4 hk-nat-report
```

运行成功后按所需间隔重复执行。报告的有效期为 60 秒，可先用 5 秒间隔，再按实际负载调整。

## 前置

安装包含条件保存功能的 xx-ui 和脚本。为定时任务账号配置 `hk-nat-read` SSH 别名及读取密钥，固定香港主机指纹。将面板的 admin API token 保存到仅任务账号可读的文件。该 token 可操作面板管理接口，留在前置。脚本拒绝非回环面板地址，也不会跟随重定向。HTTPS 面板需要本机信任且匹配回环地址的证书。检查面板的出站测试 URL 使用 HTTPS。

将下列内容保存为本机配置，地址、路径和标签替换为实际部署值。若面板设置了访问路径前缀，在 `panel_url` 中保留该前缀。若多条专线出站或向导生成的备用出站对应同一个台湾 NAT，把它们的实际标签都列入 `outbound_tags`。

```json
{
  "panel_url": "http://127.0.0.1:2053",
  "token_file": "/etc/nat-outbound-sync/panel-token",
  "ssh_alias": "hk-nat-read",
  "live_config": "/usr/local/xx-ui/bin/config.json",
  "outbound_tags": ["台湾 NAT 落地"],
  "port": 35974
}
```

```sh
python3 /usr/local/libexec/nat-outbound-sync.py sync /etc/nat-outbound-sync/taiwan.json
```

先单次运行，核对各目标仍指向正确的固定端口和拨号链。当前支持单服务器的 Shadowsocks、SOCKS、VLESS、VMess 和 Trojan 出站。托管中转的 SOCKS 配置从前置当前生成的 Xray 配置读取。每条候选链都通过 HTTPS 探测，且香港仍报告同一个地址后，脚本才提交整份条件更新。面板配置在此期间变化会返回 HTTP 412，留待下次重新读取。探测失败时保留配置。Xray 应用失败时，面板可能已保存模板。只要生成的配置仍指向旧地址，脚本下次就会重试。

## 定时执行

在台湾和前置各建立一个 `nat-outbound-sync.service`。以下为前置示例，`User` 改为已完成 SSH 和文件授权的任务账号。台湾把 `ExecStart` 改为上面的 `ssh -4 ... hk-nat-report` 命令，程序路径用 `/usr/bin/ssh`。

```ini
[Unit]
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
User=nat-sync
ExecStart=/usr/bin/python3 /usr/local/libexec/nat-outbound-sync.py sync /etc/nat-outbound-sync/taiwan.json
```

同一目录下建立 `nat-outbound-sync.timer`。先确认单次服务成功，再启用定时器。

```ini
[Timer]
OnBootSec=5s
OnUnitInactiveSec=5s
AccuracySec=1s

[Install]
WantedBy=timers.target
```

```sh
systemctl daemon-reload
systemctl start nat-outbound-sync.service
systemctl enable --now nat-outbound-sync.timer
```

结束同步时停止两端定时任务，再撤销两把同步公钥和前置 token。线路是否会在失联时直连或转入其他节点，仍取决于现有路由规则。
