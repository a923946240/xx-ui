#!/usr/bin/env python3
"""Prepare a same-host migration from 3x-ui v3.10.2 to xx-ui v3.12.0."""

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import socket
import sqlite3
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.parse
import urllib.request


SOURCE = Path("/etc/x-ui/x-ui.db")
OLD_BINARY = Path("/usr/local/x-ui/x-ui")
RELEASE = "v3.12.0"
SYSTEM_SETTINGS = (
    "webListen", "webPort", "webDomain", "webBasePath", "webCertFile", "webKeyFile",
    "subListen", "subPort", "subDomain", "subEnable", "subPath", "subURI",
    "subEncrypt", "subJsonEnable", "subJsonPath", "subJsonURI", "subClashEnable",
    "subClashPath", "subClashURI", "subCertFile", "subKeyFile", "subUpdates",
)
CERT_SETTINGS = ("webCertFile", "webKeyFile", "subCertFile", "subKeyFile")
INBOUND_FIELDS = {
    "remark": "remark", "enable": "enable", "listen": "listen", "port": "port",
    "protocol": "protocol", "settings": "settings", "streamSettings": "stream_settings",
    "tag": "tag", "sniffing": "sniffing", "expiryTime": "expiry_time", "total": "total",
}
BCRYPT = re.compile(r"\$2[aby]\$\d{2}\$[./A-Za-z0-9]{53}")


class MigrationError(Exception):
    pass


class NoRedirects(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        return None


def command(args, **kwargs):
    return subprocess.run(args, check=True, capture_output=True, text=True, **kwargs).stdout


def read_only(path):
    return sqlite3.connect(path.as_uri() + "?mode=ro", uri=True)


def read_source(db):
    db.row_factory = sqlite3.Row
    settings = dict(db.execute("SELECT key, value FROM settings"))
    accounts = [tuple(row) for row in db.execute("SELECT username, password FROM users")]
    if len(accounts) != 1 or not BCRYPT.fullmatch(accounts[0][1] or ""):
        raise MigrationError("Expected one local administrator with a bcrypt password.")
    if any(settings.get(key, "").lower() in ("1", "true", "yes") for key in (
        "twoFactorEnable", "ldapEnable", "tgBotEnable",
    )):
        raise MigrationError("Two-factor, LDAP, and bot settings require manual migration.")
    rows = db.execute("SELECT * FROM inbounds").fetchall()
    if len(rows) != 1:
        raise MigrationError("This migration currently supports exactly one inbound.")
    source = rows[0]
    inbound = {key: source[column] for key, column in INBOUND_FIELDS.items()}
    inbound["enable"] = bool(inbound["enable"])
    stream = json.loads(inbound["streamSettings"])
    clients = json.loads(inbound["settings"]).get("clients", [])
    if (not inbound["enable"] or not inbound["tag"] or inbound["protocol"] != "vless"
            or stream.get("security") != "reality"):
        raise MigrationError("Expected an enabled VLESS/REALITY inbound.")
    if not clients or not all(client.get("id") and client.get("subId") for client in clients):
        raise MigrationError("Every client must have a UUID and subscription ID.")
    if any(client.get("totalGB") for client in clients) or inbound["total"]:
        raise MigrationError("Traffic quotas require manual migration to preserve usage.")
    template = json.loads(settings["xrayTemplateConfig"])
    if any(item.get("protocol") not in ("freedom", "blackhole") for item in template["outbounds"]):
        raise MigrationError("Only direct and block template outbounds are supported.")
    if any(item.get("tag") != "api" for item in template.get("inbounds", [])):
        raise MigrationError("Extra template listeners require manual migration.")
    required = ("webPort", "webBasePath", "subPath")
    if any(not settings.get(key) for key in required):
        raise MigrationError("Could not verify production panel and subscription settings.")
    return settings, accounts, inbound, clients, template


def source_service():
    result = command([
        "systemctl", "show", "x-ui.service", "--property=MainPID,ActiveState,FragmentPath,DropInPaths",
    ])
    state = dict(line.split("=", 1) for line in result.splitlines())
    if state["ActiveState"] != "active" or int(state["MainPID"]) <= 0:
        raise MigrationError("The existing x-ui systemd service must be running.")
    binary = Path(f'/proc/{state["MainPID"]}/exe').resolve(strict=True)
    build = binary.read_bytes()
    if b"path\tgithub.com/a923946240/xx-ui/v3" in build:
        raise MigrationError("xx-ui is already running. Use the normal installer to update it.")
    if binary != OLD_BINARY or not re.search(
        rb"mod\tgithub\.com/mhsanaei/3x-ui/v3\tv3\.10\.2\t", build, re.I,
    ):
        raise MigrationError("This importer supports the systemd installation of 3x-ui v3.10.2.")
    running_env = dict(item.split(b"=", 1) for item in
                       Path(f'/proc/{state["MainPID"]}/environ').read_bytes().split(b"\0") if b"=" in item)
    if (running_env.get(b"XUI_DB_FOLDER", b"/etc/x-ui") != b"/etc/x-ui"
            or running_env.get(b"XUI_DB_TYPE", b"sqlite") != b"sqlite"):
        raise MigrationError("A custom source database location or backend requires manual migration.")
    return state


def backup(state):
    root = Path(tempfile.mkdtemp(prefix="xxui-3xui-migration-", dir="/root"))
    with read_only(SOURCE) as old, sqlite3.connect(root / "source.db") as copy:
        old.backup(copy)
        if copy.execute("PRAGMA quick_check").fetchone()[0] != "ok":
            raise MigrationError(f"SQLite backup integrity check failed in {root}.")
        settings, *_ = read_source(copy)
    paths = [Path("/usr/local/x-ui"), Path(state["FragmentPath"])]
    paths.extend(Path(path) for path in state["DropInPaths"].split())
    for key in CERT_SETTINGS:
        if settings.get(key):
            path = Path(settings[key])
            if not path.is_absolute() or not path.is_file():
                raise MigrationError(f"Configured certificate file for {key} is missing or relative.")
            paths.extend((path, path.resolve()))
    with tarfile.open(root / "source-files.tar.gz", "w:gz", compresslevel=1) as archive:
        for path in dict.fromkeys(paths):
            archive.add(path, arcname=str(path).lstrip("/"))
    print(f"Source backup verified: {root}", flush=True)
    return root


def download(root):
    arch = {"x86_64": "amd64", "aarch64": "arm64"}.get(os.uname().machine)
    if not arch:
        raise MigrationError("Migration preparation supports Linux amd64 and arm64.")
    name = f"x-ui-linux-{arch}.tar.gz"
    for asset in (name, name + ".sha256"):
        command([
            "curl", "-fsSL", "--retry", "3", "--connect-timeout", "10",
            "--speed-limit", "1024", "--speed-time", "30", "--max-time", "900",
            "-o", str(root / asset),
            f"https://github.com/a923946240/xx-ui/releases/download/{RELEASE}/{asset}",
        ])
    expected = (root / (name + ".sha256")).read_text().split()[0]
    with (root / name).open("rb") as archive:
        actual = hashlib.file_digest(archive, "sha256").hexdigest()
    if actual != expected:
        raise MigrationError("Release SHA-256 verification failed.")
    with tarfile.open(root / name) as archive:
        archive.extractall(root, filter="data")
    return root / "x-ui"


def free_ports(count):
    sockets = [socket.socket() for _ in range(count)]
    try:
        for listener in sockets:
            listener.bind(("127.0.0.1", 0))
        return [listener.getsockname()[1] for listener in sockets]
    finally:
        for listener in sockets:
            listener.close()


def set_settings(db, values):
    for key, value in values.items():
        if not db.execute("UPDATE settings SET value=? WHERE key=?", (value, key)).rowcount:
            db.execute("INSERT INTO settings (key, value) VALUES (?,?)", (key, value))


def wait_until(check, what):
    deadline = time.monotonic() + 25
    while time.monotonic() < deadline:
        try:
            return check()
        except (OSError, ValueError):
            time.sleep(0.5)
    raise MigrationError(f"Timeout waiting for {what}.")


def probe_reality(root, app, inbound_port, socks_port, stream, client):
    reality = stream["realitySettings"]
    encoded = reality["privateKey"]
    private = base64.urlsafe_b64decode(encoded + "=" * (-len(encoded) % 4))
    if len(private) != 32:
        raise MigrationError("Unexpected REALITY private key format.")
    public_der = subprocess.run(
        ["openssl", "pkey", "-inform", "DER", "-pubout", "-outform", "DER"],
        input=bytes.fromhex("302e020100300506032b656e04220420") + private,
        capture_output=True, check=True,
    ).stdout
    public = base64.urlsafe_b64encode(public_der[-32:]).decode().rstrip("=")
    user = {"id": client["id"], "encryption": "none"}
    if client.get("flow"):
        user["flow"] = client["flow"]
    config = {
        "inbounds": [{"listen": "127.0.0.1", "port": socks_port, "protocol": "socks",
                      "settings": {"auth": "noauth"}}],
        "outbounds": [{
            "protocol": "vless",
            "settings": {"vnext": [{"address": "127.0.0.1", "port": inbound_port, "users": [user]}]},
            "streamSettings": {
                "network": stream["network"], "security": "reality",
                "realitySettings": {"fingerprint": "chrome", "serverName": reality["serverNames"][0],
                                    "publicKey": public, "shortId": reality["shortIds"][0]},
            },
        }],
    }
    cores = [path for path in (app / "bin").glob("xray-linux-*") if path.is_file()]
    if len(cores) != 1:
        raise MigrationError("Could not identify the staged Xray executable.")
    with tempfile.NamedTemporaryFile(mode="w", suffix=".json", dir=root) as file:
        json.dump(config, file)
        file.flush()
        with subprocess.Popen(
            [str(cores[0]), "run", "-c", file.name], cwd=app,
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        ) as process:
            try:
                def client_ready():
                    if process.poll() is not None:
                        raise MigrationError("Xray could not start the isolated REALITY test client.")
                    socket.create_connection(("127.0.0.1", socks_port), 2).close()

                wait_until(client_ready, "REALITY test client")
                status = command([
                    "curl", "-q", "-fsS", "--noproxy", "", "--socks5-hostname",
                    f"127.0.0.1:{socks_port}", "--max-time", "15",
                    "-o", "/dev/null", "-w", "%{http_code}", "https://www.google.com/generate_204",
                ])
                if status != "204":
                    raise MigrationError("REALITY test did not return HTTP 204.")
            finally:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()


def stage_service(root, app, snapshot):
    settings, accounts, inbound, clients, template = snapshot
    db_dir = root / "candidate-db"
    log_dir = root / "candidate-log"
    db_dir.mkdir()
    log_dir.mkdir()
    web_port, sub_port, inbound_port, api_port, metrics_port, socks_port = free_ports(6)
    unit = "xxui-migration-" + root.name.rsplit("-", 1)[-1]
    env = {
        "XUI_DB_TYPE": "sqlite", "XUI_DB_FOLDER": str(db_dir),
        "XUI_BIN_FOLDER": str(app / "bin"), "XUI_LOG_FOLDER": str(log_dir),
        "XUI_PORT": str(web_port),
    }
    binary = str(app / "x-ui")
    command([binary, "migrate"], cwd=app, env=dict(os.environ, **env))
    isolated = json.loads(json.dumps(template))
    for listener in isolated.get("inbounds", []):
        listener.update(listen="127.0.0.1", port=api_port)
    if isolated.get("metrics", {}).get("listen"):
        isolated["metrics"]["listen"] = f"127.0.0.1:{metrics_port}"
    with sqlite3.connect(db_dir / "x-ui.db") as db:
        set_settings(db, {
            "webListen": "127.0.0.1", "webPort": str(web_port), "webBasePath": "/",
            "subListen": "127.0.0.1", "subPort": str(sub_port), "subEnable": "true",
            "subPath": settings["subPath"], "xrayTemplateConfig": json.dumps(isolated),
        })
    credentials = command(
        [binary, "setting", "-getApiToken", "-tokenName", "migration-check", "-tokenScope", "admin"],
        cwd=app, env=dict(os.environ, **env),
    )
    match = re.search(r"(?m)^apiToken:\s*(\S+)", credentials)
    if not match:
        raise MigrationError("Could not initialize the isolated panel API.")
    token = match.group(1)
    loopback = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirects())

    def panel_api(route, fields=None):
        req = urllib.request.Request(
            f"http://127.0.0.1:{web_port}/panel/api/{route}",
            data=urllib.parse.urlencode(fields or {}).encode(),
            headers={"Authorization": f"Bearer {token}"},
        )
        with loopback.open(req, timeout=5) as response:
            payload = json.load(response)
        if payload.get("success") is not True:
            raise MigrationError(f"Isolated panel rejected {route}.")
        return payload.get("obj")

    opts = [
        "systemd-run", "--quiet", "--unit", unit, "--property=RuntimeMaxSec=5min",
        "--property=NoNewPrivileges=yes", "--property=ProtectSystem=strict",
        f"--property=ReadWritePaths={root}", f"--working-directory={app}",
    ]
    opts.extend(f"--setenv={key}={value}" for key, value in env.items())
    command(opts + [binary, "run"])
    try:
        wait_until(lambda: panel_api("xray/"), "isolated panel")
        panel_api("inbounds/import", {"data": json.dumps({
            **inbound, "listen": "127.0.0.1", "port": inbound_port,
        })})
        command(["systemctl", "restart", unit])
        wait_until(lambda: panel_api("xray/"), "isolated panel restart")

        def generated():
            config = json.loads((app / "bin" / "config.json").read_text())
            found = [entry for entry in config.get("inbounds", []) if entry["port"] == inbound_port]
            if len(found) != 1:
                raise ValueError("inbound not yet generated")
            return found[0]

        entry = wait_until(generated, "generated inbound")
        original = json.loads(inbound["streamSettings"])["realitySettings"]
        reality = entry["streamSettings"]["realitySettings"]
        if (entry["listen"] != "127.0.0.1" or entry["tag"] != inbound["tag"]
                or reality["privateKey"] != original["privateKey"]
                or reality["shortIds"] != original["shortIds"]
                or {(c["id"], c.get("flow", "")) for c in entry["settings"]["clients"]}
                != {(c["id"], c.get("flow", "")) for c in clients}):
            raise MigrationError("Generated inbound does not match the original identity.")
        wait_until(lambda: socket.create_connection(("127.0.0.1", inbound_port), 2).close(), "Xray listener")
        for client in clients:
            url = (f'http://127.0.0.1:{sub_port}' + settings["subPath"].rstrip("/") + "/"
                   + urllib.parse.quote(client["subId"], safe=""))
            with loopback.open(url, timeout=6) as response:
                body = response.read().strip()
            if not body.startswith(b"vless://"):
                body = base64.b64decode(body + b"=" * (-len(body) % 4))
            if ("vless://" + client["id"] + "@").encode() not in body:
                raise MigrationError("Isolated subscription does not contain the original client.")
        probe_reality(root, app, inbound_port, socks_port, json.loads(inbound["streamSettings"]), clients[0])
        print("Isolated REALITY HTTPS and original subscription paths passed.", flush=True)
    finally:
        command(["systemctl", "stop", unit])

    with sqlite3.connect(db_dir / "x-ui.db") as db:
        set_settings(db, {
            **{key: settings[key] for key in SYSTEM_SETTINGS if key in settings},
            "webListen": settings.get("webListen", ""), "subListen": settings.get("subListen", ""),
            "webPort": settings["webPort"], "subPort": settings.get("subPort", "2096"),
            "xrayTemplateConfig": json.dumps(template),
        })
        if db.execute("UPDATE inbounds SET listen=?, port=?", (inbound["listen"], inbound["port"])).rowcount != 1:
            raise MigrationError("Candidate database does not contain exactly one migrated inbound.")
        if db.execute("UPDATE users SET username=?, password=?", accounts[0]).rowcount != 1:
            raise MigrationError("Could not retain the original administrator.")
        # The bearer token is used only by the isolated rehearsal.
        db.execute("DELETE FROM api_tokens WHERE name=?", ("migration-check",))
        if db.execute("PRAGMA quick_check").fetchone()[0] != "ok":
            raise MigrationError("Candidate database failed SQLite integrity check.")
    print(f"Candidate prepared: {db_dir / 'x-ui.db'}")
    print("Temporary listeners stopped. Original service was left running.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("check", "backup", "prepare"), nargs="?", default="check")
    args = parser.parse_args()
    if os.geteuid() != 0:
        parser.exit(1, "Run the migration as root.\n")
    os.umask(0o077)
    try:
        state = source_service()
        with read_only(SOURCE) as db:
            _, _, inbound, clients, _ = read_source(db)
        print(f'Preflight: 3x-ui v3.10.2, SQLite, VLESS/REALITY port {inbound["port"]}, {len(clients)} client(s).')
        if args.action != "check":
            root = backup(state)
            if args.action == "prepare":
                with read_only(root / "source.db") as copied:
                    snapshot = read_source(copied)
                stage_service(root, download(root), snapshot)
    except MigrationError as err:
        parser.exit(1, f"Migration stopped: {err}\n")
    except (OSError, ValueError, TypeError, KeyError, sqlite3.Error, subprocess.SubprocessError,
            tarfile.TarError, tarfile.FilterError) as err:
        parser.exit(1, f"Migration stopped ({type(err).__name__}). The original service was not stopped.\n")


if __name__ == "__main__":
    main()
