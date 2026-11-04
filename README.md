# xx-ui

English · [简体中文](README.zh_CN.md)

xx-ui is a web panel for managing Xray. It extends 3x-ui v3.11.0 with three additional tunnel clients, per-rule outbound failover, and a charcoal theme. [Project repository](https://github.com/a923946240/xx-ui)

## Route traffic through a tunnel

Clients connect to an existing Xray inbound. On the **Outbounds** page, add a NaiveProxy, mieru, or TrustTunnel exit and give it a tag. Choose that tag in a routing rule to send matching TCP connections through the tunnel.

```text
client → Xray inbound → selected outbound → local tunnel client → remote tunnel server → destination
```

Install the matching tunnel client on the Linux host that runs the panel and supply a remote endpoint for it. The managed exits support TCP CONNECT. [Setup and supported transports (Chinese)](docs/external-outbounds.md) · [External outbounds API](docs/content/docs/en/reference/api/external-outbounds.mdx)

## Give a routing rule a backup

For example, open **Routing → Routing Rules**, edit the rule for `domain:google.com`, select A as the outbound tag and B as the backup outbound, then save the rule and the page. Xray sends new connections through A. After a failed health check it sends new connections through B. Once A passes a check again, new connections return to A. An existing connection must reconnect to change exits.

By default Xray probes `https://www.google.com/generate_204` through A every 15 seconds. The interval is configurable through the Xray observatory. An existing observatory or burst observatory keeps its probe settings. [Routing guide (Chinese)](docs/content/docs/zh/operations/outbounds-routing.mdx)

## Appearance

The dark theme combines charcoal surfaces, frosted panels, and a subtle particle background. The particle animation follows the system’s reduced-motion preference.

## Build from source on Linux

From the repository root, use Go 1.27.3, Node.js 26, npm 11 or later, and a C compiler for SQLite. [Go requirements](go.mod) · [Frontend requirements](frontend/package.json)

```sh
npm --prefix frontend ci
npm --prefix frontend run build
go build -o xx-ui .
```

The resulting `xx-ui` executable includes the web interface. To run the proxy, place the Xray executable and the geodata used by your routing rules in `bin/` or the directory set by `XUI_BIN_FOLDER`. On Linux amd64 the core executable is named `xray-linux-amd64`. Install any external tunnel clients you plan to use, then start the panel with `./xx-ui`. [External client installer](scripts/install-external-clients.sh)

### Install the external tunnel clients

The web panel does not install or upload NaiveProxy, mieru, or TrustTunnel executables. Linux build artifacts include `scripts/install-external-clients.sh`. If you deploy only the compiled panel executable, copy that script into `scripts/` under the panel installation directory as well.

From the panel installation directory, run as a user who can write to the binary directory:

```sh
bash scripts/install-external-clients.sh "${XUI_BIN_FOLDER:-./bin}/external-proxies" all
```

Replace `all` with `naive`, `mieru`, or `trusttunnel` to install one client. The script downloads pinned official releases and verifies their SHA-256 hashes. The panel looks for the executable names `naive`, `mieru`, and `trusttunnel` in that directory. Installing them does not create an exit or restart the panel.

This repository currently provides source builds. The original installer and deployment examples are retained as upstream references. A private repository also requires authentication for release downloads, which the panel's updater has not been configured to provide.

## Credits and license

xx-ui builds on the work of **MHSanaei and the 3x-ui contributors** and uses Xray-core from the XTLS project. This fork is distributed under the GNU General Public License v3 (GPLv3). [3x-ui v3.11.0](https://github.com/MHSanaei/3x-ui/tree/v3.11.0) · [Xray-core](https://github.com/XTLS/Xray-core) · [License](LICENSE)
