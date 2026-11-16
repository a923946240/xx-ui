#!/usr/bin/env python3
"""Relay a NAT IPv4 over SSH and update selected xx-ui outbounds."""

import argparse
import ipaddress
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode, urlsplit
from urllib.request import build_opener, HTTPRedirectHandler, ProxyHandler, Request


STATUS_PATH = Path("/var/lib/xx-ui/nat-sync/status.json")


def public_ipv4(value):
    ip = ipaddress.IPv4Address(value)
    if not ip.is_global or ip.is_multicast:
        raise ValueError("reported address must be a public unicast IPv4")
    return str(ip)


def write_json_atomic(path, data):
    with tempfile.NamedTemporaryFile(mode="w", dir=path.parent, delete=False) as tmp:
        tmp_path = Path(tmp.name)
        try:
            json.dump(data, tmp)
            tmp.close()
            tmp_path.replace(path)
        finally:
            if tmp_path.exists():
                tmp_path.unlink()


def report(path):
    connection = os.environ.get("SSH_CONNECTION", "").split()
    if len(connection) != 4:
        raise ValueError("missing SSH peer address")
    address = public_ipv4(connection[0])
    write_json_atomic(path, {"address": address, "seen_at": time.time()})


def read_report(path, max_age):
    entry = json.loads(path.read_text())
    if not 0 <= time.time() - entry["seen_at"] <= max_age:
        raise ValueError("NAT report expired")
    return public_ipv4(entry["address"])


class NoRedirects(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def panel_request(base, token, route, fields=None, revision=None):
    headers = {"Authorization": "Bearer " + token}
    if revision is not None:
        headers["If-Match"] = revision
    req = Request(base + "/panel/api/xray/" + route,
                  data=urlencode(fields or {}).encode(), headers=headers, method="POST")
    with build_opener(ProxyHandler({}), NoRedirects()).open(req, timeout=60) as response:
        result = json.load(response)
        if result.get("success") is not True:
            raise ValueError("panel rejected " + (route or "config read"))
        return result.get("obj"), response.headers.get("ETag")


def select_outbound(outbounds, tag):
    matches = [item for item in outbounds if item.get("tag") == tag]
    if len(matches) != 1:
        raise ValueError("outbound tag absent or ambiguous")
    return matches[0]


def endpoint(outbound, port):
    containers = {"shadowsocks": "servers", "socks": "servers", "vless": "vnext",
                  "vmess": "vnext", "trojan": "servers"}
    name = containers.get(outbound.get("protocol"))
    servers = outbound.get("settings", {}).get(name)
    if not isinstance(servers, list) or len(servers) != 1 or servers[0].get("port") != port:
        raise ValueError("expected one supported server with the configured fixed port")
    return servers[0]


def upstream(outbound):
    stream = outbound.get("streamSettings") or {}
    return (stream.get("sockopt") or {}).get("dialerProxy")


def probe_context(target, templates, live):
    chain = [target]
    current = target
    visited = {target["tag"]}
    while (tag := upstream(current)):
        if tag in visited:
            raise ValueError("dialerProxy cycle")
        visited.add(tag)
        matches = [item for item in templates if item.get("tag") == tag]
        current = select_outbound(templates if matches else live, tag)
        if current.get("protocol") in ("freedom", "blackhole"):
            raise ValueError("unexpected direct or blocked hop")
        chain.append(current)
    if len(chain) == 1:
        raise ValueError("target must have a dialerProxy")
    return chain


def relay_address(relay):
    if not relay or relay.startswith("-") or any(ch.isspace() for ch in relay):
        raise ValueError("invalid SSH host alias")
    return public_ipv4(subprocess.run(
        ["ssh", "-n", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes",
         "-o", "ConnectTimeout=4", relay],
        capture_output=True, text=True, check=True, timeout=8,
    ).stdout.strip())


def sync(config):
    status = {}
    publish = STATUS_PATH.parent.is_dir()
    if publish:
        try:
            previous = json.loads(STATUS_PATH.read_text())
            if isinstance(previous, dict):
                for key in ("currentIp", "candidateIp", "probeIp", "probeResult", "probeAt", "appliedAt"):
                    if key in previous:
                        status[key] = previous[key]
        except (OSError, ValueError):
            pass
    try:
        sync_outbound(config, status)
    finally:
        if publish:
            status["updatedAt"] = int(time.time())
            try:
                write_json_atomic(STATUS_PATH, status)
            except OSError:
                print("NAT sync could not write its status file", file=sys.stderr)


def sync_outbound(config, status):
    status["state"] = "config_error"
    base = config["panel_url"].rstrip("/")
    parsed = urlsplit(base)
    if (parsed.scheme not in ("http", "https") or parsed.hostname not in ("127.0.0.1", "::1")
            or parsed.username or parsed.password or parsed.query or parsed.fragment):
        raise ValueError("panel_url must use a loopback address")
    relay = config["ssh_alias"]
    status["state"] = "relay_error"
    address = relay_address(relay)
    status.update(candidateIp=address, state="config_error")
    token = Path(config["token_file"]).read_text().strip()
    payload, revision = panel_request(base, token, "")
    if not revision:
        raise ValueError("panel does not support conditional Xray updates")
    panel = json.loads(payload)
    template = panel["xraySetting"]
    outbounds = template["outbounds"]
    live = json.loads(Path(config["live_config"]).read_text())["outbounds"]
    tags = config["outbound_tags"]
    port = config["port"]
    if not isinstance(tags, list) or not tags or len(set(tags)) != len(tags):
        raise ValueError("configure distinct outbound tags")
    if type(port) is not int or not 1 <= port <= 65535:
        raise ValueError("invalid fixed port")
    targets = [select_outbound(outbounds, tag) for tag in tags]
    live_addresses = [
        endpoint(select_outbound(live, tag), port).get("address") for tag in tags
    ]
    status.pop("currentIp", None)
    if all(ip == live_addresses[0] for ip in live_addresses):
        try:
            status["currentIp"] = public_ipv4(live_addresses[0])
        except ValueError:
            pass
    changed = False
    for target, live_address in zip(targets, live_addresses):
        server = endpoint(target, port)
        if server.get("address") != address or live_address != address:
            changed = True
        server["address"] = address
    if not changed:
        status["state"] = "in_sync"
        return
    if urlsplit(panel["outboundTestUrl"]).scheme != "https":
        raise ValueError("configure an HTTPS outbound test URL")
    status.update(state="probe_error", probeIp=address, probeAt=int(time.time()), probeResult="failed")
    for target in targets:
        chain = probe_context(target, outbounds, live)
        results, _ = panel_request(base, token, "testOutbounds", {
            "outbounds": json.dumps([target]),
            "allOutbounds": json.dumps(chain),
            "mode": "http",
        })
        if (not isinstance(results, list) or len(results) != 1
                or results[0].get("tag") != target["tag"]
                or results[0].get("success") is not True
                or not 200 <= results[0].get("httpStatus", 0) < 400):
            raise ValueError("candidate chain probe failed")
    status.update(state="relay_error", probeAt=int(time.time()), probeResult="passed")
    if relay_address(relay) != address:
        status["state"] = "candidate_changed"
        raise ValueError("NAT address changed during probe")
    status["state"] = "apply_error"
    panel_request(base, token, "update", {
        "xraySetting": json.dumps(template),
        "outboundTestUrl": panel["outboundTestUrl"],
    }, revision)
    applied = json.loads(Path(config["live_config"]).read_text())["outbounds"]
    if any(endpoint(select_outbound(applied, tag), port).get("address") != address for tag in tags):
        raise ValueError("running config has not applied the new address")
    status.update(state="in_sync", currentIp=address, appliedAt=int(time.time()))
    print("NAT outbound address applied")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    cmds = parser.add_subparsers(dest="command", required=True)
    for name in ("report", "read", "sync"):
        command = cmds.add_parser(name)
        command.add_argument("file", type=Path)
        if name == "read":
            command.add_argument("--max-age", type=int, default=60)
    args = parser.parse_args()
    try:
        if args.command == "report":
            report(args.file)
        elif args.command == "read":
            print(read_report(args.file, args.max_age))
        else:
            sync(json.loads(args.file.read_text()))
    except HTTPError as err:
        print(f"NAT sync: panel HTTP {err.code}", file=sys.stderr)
        return 1
    except (OSError, URLError, subprocess.SubprocessError, TypeError, KeyError, ValueError) as err:
        print(f"NAT sync failed ({type(err).__name__})", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
