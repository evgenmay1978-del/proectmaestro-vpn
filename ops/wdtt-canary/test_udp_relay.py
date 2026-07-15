import socket
import threading
import time
import unittest

from udp_relay import UdpRelay


class UdpRelayTest(unittest.TestCase):
    def test_round_trip_and_two_independent_peers(self) -> None:
        echo = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        echo.bind(("127.0.0.1", 0))
        echo.settimeout(0.2)
        stop_echo = threading.Event()

        def echo_loop() -> None:
            while not stop_echo.is_set():
                try:
                    payload, source = echo.recvfrom(65535)
                    echo.sendto(payload, source)
                except socket.timeout:
                    pass

        echo_thread = threading.Thread(target=echo_loop, daemon=True)
        echo_thread.start()
        relay = UdpRelay("127.0.0.1", 0, "127.0.0.1", echo.getsockname()[1], idle_timeout=5)
        relay_thread = threading.Thread(target=relay.run, daemon=True)
        relay_thread.start()

        clients = [socket.socket(socket.AF_INET, socket.SOCK_DGRAM) for _ in range(2)]
        try:
            for index, client in enumerate(clients):
                client.settimeout(2)
                payload = f"peer-{index}".encode()
                client.sendto(payload, relay.listen_address)
                received, source = client.recvfrom(65535)
                self.assertEqual(payload, received)
                self.assertEqual(relay.listen_address[1], source[1])
            self.assertEqual(2, len(relay.peers))
        finally:
            for client in clients:
                client.close()
            relay.stop()
            relay_thread.join(timeout=3)
            stop_echo.set()
            echo_thread.join(timeout=1)
            echo.close()

        self.assertFalse(relay_thread.is_alive())

    def test_idle_peer_is_removed(self) -> None:
        echo = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        echo.bind(("127.0.0.1", 0))
        relay = UdpRelay("127.0.0.1", 0, "127.0.0.1", echo.getsockname()[1], idle_timeout=0.05)
        client = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        try:
            client.sendto(b"one-way", relay.listen_address)
            relay.listener.settimeout(1)
            payload, source = relay.listener.recvfrom(65535)
            relay._peer_for(source, time.monotonic())
            relay._expire(time.monotonic() + 1)
            self.assertEqual({}, relay.peers)
        finally:
            client.close()
            relay.close()
            echo.close()
