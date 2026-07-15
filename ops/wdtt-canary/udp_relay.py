#!/usr/bin/env python3
"""Small UDP NAT relay used only by the isolated WDTT canary.

The WDTT container has no published Docker ports. This process owns host UDP
56000 and forwards each external peer to the container through a separate
connected socket, preserving independent DTLS sessions without changing host
firewall rules.
"""

from __future__ import annotations

import argparse
import selectors
import signal
import socket
import time
from dataclasses import dataclass


@dataclass
class Peer:
    backend: socket.socket
    last_seen: float


class UdpRelay:
    def __init__(
        self,
        listen_host: str,
        listen_port: int,
        backend_host: str,
        backend_port: int,
        idle_timeout: float = 120.0,
        max_peers: int = 128,
    ) -> None:
        self.selector = selectors.DefaultSelector()
        self.listener = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        self.listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.listener.setblocking(False)
        self.listener.bind((listen_host, listen_port))
        self.listen_address = self.listener.getsockname()
        self.backend_address = (socket.gethostbyname(backend_host), backend_port)
        self.idle_timeout = idle_timeout
        self.max_peers = max_peers
        self.peers: dict[tuple[str, int], Peer] = {}
        self.running = False
        self.selector.register(self.listener, selectors.EVENT_READ, None)

    def _close_peer(self, client: tuple[str, int]) -> None:
        peer = self.peers.pop(client, None)
        if peer is None:
            return
        try:
            self.selector.unregister(peer.backend)
        except (KeyError, ValueError):
            pass
        peer.backend.close()

    def _peer_for(self, client: tuple[str, int], now: float) -> Peer | None:
        peer = self.peers.get(client)
        if peer is not None:
            peer.last_seen = now
            return peer
        if len(self.peers) >= self.max_peers:
            return None
        backend = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        backend.setblocking(False)
        backend.connect(self.backend_address)
        peer = Peer(backend=backend, last_seen=now)
        self.peers[client] = peer
        self.selector.register(backend, selectors.EVENT_READ, client)
        return peer

    def _expire(self, now: float) -> None:
        for client, peer in list(self.peers.items()):
            if now - peer.last_seen > self.idle_timeout:
                self._close_peer(client)

    def run(self) -> None:
        self.running = True
        print(
            f"READY listen={self.listen_address[0]}:{self.listen_address[1]} "
            f"backend={self.backend_address[0]}:{self.backend_address[1]}",
            flush=True,
        )
        while self.running:
            now = time.monotonic()
            for key, _ in self.selector.select(timeout=1.0):
                if key.data is None:
                    try:
                        payload, client = self.listener.recvfrom(65535)
                    except BlockingIOError:
                        continue
                    peer = self._peer_for(client, now)
                    if peer is None:
                        continue
                    try:
                        peer.backend.send(payload)
                    except OSError:
                        self._close_peer(client)
                else:
                    client = key.data
                    peer = self.peers.get(client)
                    if peer is None:
                        continue
                    try:
                        payload = peer.backend.recv(65535)
                        self.listener.sendto(payload, client)
                        peer.last_seen = now
                    except BlockingIOError:
                        continue
                    except OSError:
                        self._close_peer(client)
            self._expire(time.monotonic())

    def stop(self) -> None:
        self.running = False

    def close(self) -> None:
        self.stop()
        for client in list(self.peers):
            self._close_peer(client)
        try:
            self.selector.unregister(self.listener)
        except (KeyError, ValueError):
            pass
        self.listener.close()
        self.selector.close()


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--listen-host", default="0.0.0.0")
    parser.add_argument("--listen-port", type=int, default=56000)
    parser.add_argument("--backend-host", required=True)
    parser.add_argument("--backend-port", type=int, default=56000)
    parser.add_argument("--idle-timeout", type=float, default=120.0)
    parser.add_argument("--max-peers", type=int, default=128)
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    if not 1 <= args.listen_port <= 65535 or not 1 <= args.backend_port <= 65535:
        raise SystemExit("invalid UDP port")
    if args.idle_timeout <= 0 or not 1 <= args.max_peers <= 4096:
        raise SystemExit("invalid relay limits")
    relay = UdpRelay(
        args.listen_host,
        args.listen_port,
        args.backend_host,
        args.backend_port,
        args.idle_timeout,
        args.max_peers,
    )
    signal.signal(signal.SIGINT, lambda *_: relay.stop())
    signal.signal(signal.SIGTERM, lambda *_: relay.stop())
    try:
        relay.run()
    finally:
        relay.close()


if __name__ == "__main__":
    main()
