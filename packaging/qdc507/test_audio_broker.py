import importlib.machinery
import socket
import threading
import unittest
from pathlib import Path

module = importlib.machinery.SourceFileLoader(
    "audio_broker", str(Path(__file__).with_name("hideck-qdc507-audio-broker"))).load_module()


class BrokerTests(unittest.TestCase):
    def test_ready_release_and_exclusive_owner(self):
        operations = []
        broker = module.Broker(lambda: operations.append("clean"),
                               lambda: operations.append("start"), lambda: True)
        client, server = socket.socketpair()
        client.settimeout(2)
        worker = threading.Thread(target=broker.handle, args=(server,))
        worker.start()
        self.assertEqual(client.recv(32), b"READY\n")
        other, other_server = socket.socketpair()
        with other:
            broker.handle(other_server)
            self.assertEqual(other.recv(32), b"BUSY\n")
        client.close()
        worker.join(2)
        self.assertFalse(worker.is_alive())
        self.assertEqual(operations, ["clean", "start", "clean"])

    def test_disconnect_during_startup_cleans(self):
        operations = []
        starting = threading.Event()
        def start():
            operations.append("start")
            starting.set()
        broker = module.Broker(lambda: operations.append("clean"),
                               start, lambda: False)
        client, server = socket.socketpair()
        worker = threading.Thread(target=broker.handle, args=(server,))
        worker.start()
        self.assertTrue(starting.wait(2))
        client.close()
        worker.join(2)
        self.assertFalse(worker.is_alive())
        self.assertEqual(operations, ["clean", "start", "clean"])

    def test_broker_shutdown_releases_live_route(self):
        operations = []
        broker = module.Broker(lambda: operations.append("clean"), lambda: None, lambda: True)
        client, server = socket.socketpair()
        client.settimeout(2)
        worker = threading.Thread(target=broker.handle, args=(server,))
        worker.start()
        with client:
            self.assertEqual(client.recv(32), b"READY\n")
            broker.stopping.set()
            self.assertEqual(client.recv(32), b"")
        worker.join(2)
        self.assertFalse(worker.is_alive())
        self.assertEqual(operations, ["clean", "clean"])

    def test_start_failure_cleans_and_releases_lock(self):
        operations = []
        def fail():
            raise RuntimeError("test startup failure")
        broker = module.Broker(lambda: operations.append("clean"), fail, lambda: False)
        client, server = socket.socketpair()
        with client:
            broker.handle(server)
            self.assertEqual(client.recv(32), b"ERROR\n")
        self.assertEqual(operations, ["clean", "clean"])
        self.assertFalse(broker.lock.locked())


if __name__ == "__main__":
    unittest.main()
