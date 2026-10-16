#!/usr/bin/env python3
"""Prepare an Infinity-only WireGuard peer without starting services.

Privileged operation limited to an EXCLUSIVE 0600 /etc/wireguard/wg-infinity.conf
write on an explicitly chosen host. No firewall, DNS, routing or service action.
Never print or pass the private key as a command-line argument.
"""
from __future__ import annotations

import argparse
import base64
import ipaddress
import os
from pathlib import Path
import re
import stat
import subprocess
import sys

DIRECTORY = Path("/etc/wireguard")
CONFIG = DIRECTORY / "wg-infinity.conf"
ADDRESS = {
    "vps": ("10.88.88.1/30", "10.88.88.2/32"),
    "srv-ia": ("10.88.88.2/30", "10.88.88.1/32"),
}
PLANNED_NETWORK = ipaddress.IPv4Network("10.88.88.0/30")
PORT = 51822
KEY_PATTERN = re.compile(r"^[A-Za-z0-9+/]{43}=$")


class UnsafeEnvironment(ValueError):
    pass


def canonical_key(value: str) -> str:
    value = value.strip()
    if not KEY_PATTERN.fullmatch(value):
        raise UnsafeEnvironment("WireGuard public key must be a 44-character base64 key")
    try:
        raw = base64.b64decode(value, validate=True)
    except ValueError as exc:
        raise UnsafeEnvironment("invalid base64 peer key") from exc
    if len(raw) != 32 or base64.b64encode(raw).decode() != value:
        raise UnsafeEnvironment("invalid WireGuard public key length/encoding")
    return value


def ensure_root_owned_path(path: Path, *, is_dir: bool, private: bool = False) -> None:
    info = path.lstat()
    if stat.S_ISLNK(info.st_mode):
        raise UnsafeEnvironment(f"symbolic link forbidden: {path}")
    if info.st_uid != 0:
        raise UnsafeEnvironment(f"not root-owned: {path}")
    if is_dir:
        if not stat.S_ISDIR(info.st_mode) or info.st_mode & 0o022:
            raise UnsafeEnvironment(f"unsafe directory permissions: {path}")
    elif not stat.S_ISREG(info.st_mode):
        raise UnsafeEnvironment(f"not a regular file: {path}")
    elif private and stat.S_IMODE(info.st_mode) != 0o600:
        raise UnsafeEnvironment(f"private file must be exactly 0600: {path}")


def check_local_route_collision() -> None:
    proc = subprocess.run(["ip", "-4", "route", "show", "table", "all"],
                          text=True, capture_output=True, timeout=10, check=False)
    if proc.returncode:
        raise UnsafeEnvironment("cannot inspect local IPv4 routes, refusing config")
    skip = {"default", "local", "broadcast", "unreachable", "prohibit", "throw", "blackhole", "nat"}
    for line in proc.stdout.splitlines():
        bits = line.split()
        if not bits:
            continue
        first = bits[0]
        if first in skip:
            continue
        try:
            network = ipaddress.IPv4Network(first, strict=False)
        except ValueError:
            raise UnsafeEnvironment("cannot safely parse local route: " + first)
        if network.overlaps(PLANNED_NETWORK):
            raise UnsafeEnvironment("existing route collides with planned 10.88.88.0/30 network")


def check_not_running() -> None:
    proc = subprocess.run(["ip", "link", "show", "dev", "wg-infinity"],
                          capture_output=True, text=True, timeout=10, check=False)
    if proc.returncode == 0:
        raise UnsafeEnvironment("wg-infinity interface already exists; do not overwrite")
    if "does not exist" not in proc.stderr and "Cannot find device" not in proc.stderr:
        raise UnsafeEnvironment("could not verify interface is absent")


def build_config(role: str, *, private_key: str, peer_key: str, endpoint_ip: str | None) -> str:
    address, peer_address = ADDRESS[role]
    lines = ["[Interface]", f"Address = {address}", f"PrivateKey = {private_key}"]
    if role == "vps":
        lines.append(f"ListenPort = {PORT}")
    lines += ["", "[Peer]", f"PublicKey = {peer_key}", f"AllowedIPs = {peer_address}"]
    if role == "srv-ia":
        if endpoint_ip is None:
            raise UnsafeEnvironment("the SRV-IA must know the VPS public IPv4 endpoint")
        try:
            ip = ipaddress.IPv4Address(endpoint_ip)
        except ipaddress.AddressValueError as exc:
            raise UnsafeEnvironment("endpoint must be a valid IPv4 address") from exc
        if ip.is_unspecified or ip.is_multicast or ip.is_loopback or ip.is_link_local:
            raise UnsafeEnvironment("endpoint cannot be multicast/loopback/link-local")
        lines.extend([f"Endpoint = {ip}:{PORT}", "PersistentKeepalive = 25"])
    elif endpoint_ip is not None:
        raise UnsafeEnvironment("VPS must not be configured with an SRV-IA endpoint behind NAT")
    return "\n".join(lines) + "\n"


def prepare(role: str, peer_key: str, endpoint: str | None) -> None:
    if os.geteuid() != 0:
        raise UnsafeEnvironment("this step must be executed by an operator as root")
    if role not in ADDRESS:
        raise UnsafeEnvironment("invalid host role")
    peer_key = canonical_key(peer_key)
    if os.path.lexists(CONFIG):
        raise UnsafeEnvironment("wg-infinity.conf already exists; NO overwrite")
    for path in (Path("/etc"), DIRECTORY):
        ensure_root_owned_path(path, is_dir=True)
    private_path = DIRECTORY / f"infinity-{role}.key"
    public_path = DIRECTORY / f"infinity-{role}.pub"
    ensure_root_owned_path(private_path, is_dir=False, private=True)
    ensure_root_owned_path(public_path, is_dir=False)

    private_key = private_path.read_text(encoding="ascii").strip()
    public_key = canonical_key(public_path.read_text(encoding="ascii"))
    # Compare the stored keypair without printing the private key in any logs.
    process = subprocess.run(["wg", "pubkey"], input=private_key + "\n", capture_output=True,
                             text=True, timeout=10, check=False)
    if process.returncode or process.stdout.strip() != public_key:
        raise UnsafeEnvironment("local WireGuard private/public keypair does not match")
    if peer_key == public_key:
        raise UnsafeEnvironment("peer key equals this host's own public key")

    check_not_running()
    check_local_route_collision()
    content = build_config(role, private_key=private_key, peer_key=peer_key,
                           endpoint_ip=endpoint)

    os.umask(0o077)
    fd = None
    try:
        fd = os.open(CONFIG, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, "w", encoding="ascii") as handle:
            fd = None
            handle.write(content)
            handle.flush()
            os.fsync(handle.fileno())
    except BaseException:
        if fd is not None:
            os.close(fd)
        # The O_EXCL open is the only step that can create this path.
        # Preexisting state must never be deleted; leave incomplete output in
        # place for operator inspection rather than trying an unsafe cleanup.
        raise
    print(f"OK: root-owned 0600 {CONFIG} created for {role}.")
    print("NOT ACTIVATED: no WireGuard interface, route, firewall or service was changed.")
    print("Next: verify UFW/ports before enabling wg-quick@wg-infinity.")


def main() -> int:
    parser = argparse.ArgumentParser(description="Prepare one isolated WireGuard Infinity peer offline")
    parser.add_argument("--role", required=True, choices=tuple(ADDRESS))
    parser.add_argument("--peer-public-key", required=True)
    parser.add_argument("--endpoint-ip", help="public IPv4 address of VPS, SRV-IA role only")
    args = parser.parse_args()
    try:
        prepare(args.role, args.peer_public_key, args.endpoint_ip)
    except (UnsafeEnvironment, OSError, UnicodeError, subprocess.TimeoutExpired) as exc:
        print("INFINITY WIREGUARD CONFIG DENIED: " + str(exc), file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
