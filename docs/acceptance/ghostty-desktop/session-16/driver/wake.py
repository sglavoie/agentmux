#!/usr/bin/env python3
"""Sends a wake-on-LAN packet for one hardware address, on the Studio's own
network. It is a network packet, not input: nothing on the Studio's desktop
is touched. usage: wake.py MAC BROADCAST_ADDRESS"""
import socket, sys

mac = bytes.fromhex(sys.argv[1].replace(":", "").replace("-", "").zfill(12)) if ":" not in sys.argv[1] else bytes(int(p, 16) for p in sys.argv[1].split(":"))
packet = b"\xff" * 6 + mac * 16
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)
for port in (9, 7):
    for address in sys.argv[2:]:
        s.sendto(packet, (address, port))
