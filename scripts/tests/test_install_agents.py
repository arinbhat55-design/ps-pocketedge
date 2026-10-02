"""Execute installer logic in a temp directory with mocked OS/service commands.

Run: python3 -m unittest discover -s scripts/tests -v
Only install locations are rewritten; argument parsing and generated files use
production script logic. No system services or user home files are modified.
"""
import os
from pathlib import Path
import plistlib
import shutil
import subprocess
import tempfile
import unittest


SCRIPTS = Path(__file__).resolve().parents[1]


class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bin = self.root / "mock-bin"
        self.bin.mkdir()
        self.calls = self.root / "service-calls"
        self.agent = self.root / "pe-agent"
        self.agent.write_text("#!/bin/sh\nexit 0\n")
        self.env = dict(os.environ)
        for name in ("DOCKER_HOST", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY", "DOCKER_API_VERSION"):
            self.env.pop(name, None)
        self.env.update(
            PATH=f"{self.bin}:/usr/bin:/bin",
            TEST_INSTALL_HOME=str(self.root / "home"),
            TEST_SERVICE_CALLS=str(self.calls),
            TEST_OS="Linux",
            TEST_UID="0",
            TEST_SOCKET="/tmp/podman-machine.sock",
            TEST_PING_FAIL="0",
        )
        self.mock("uname", 'case "$1" in -s) echo "$TEST_OS";; -m) echo x86_64;; esac')
        self.mock("id", 'echo "$TEST_UID"')
        self.mock("systemctl", 'echo "systemctl $*" >> "$TEST_SERVICE_CALLS"')
        self.mock("launchctl", 'echo "launchctl $*" >> "$TEST_SERVICE_CALLS"')
        self.mock("podman", 'printf "%s\\n" "$TEST_SOCKET"')
        self.mock("curl", 'test "$TEST_PING_FAIL" = 0')

    def mock(self, name, body):
        path = self.bin / name
        path.write_text(f"#!/bin/sh\nset -eu\n{body}\n")
        path.chmod(0o755)

    def run_installer(self, mac=False, args=()):
        name = "install-agent-macos.sh" if mac else "install-agent.sh"
        script = (SCRIPTS / name).read_text()
        if mac:
            self.env.update(TEST_OS="Darwin", TEST_UID="501")
            script = script.replace("${HOME}", "${TEST_INSTALL_HOME}").replace("$HOME", "$TEST_INSTALL_HOME")
        else:
            script = script.replace("/usr/local/bin", str(self.root / "install-bin"))
            script = script.replace("/etc/pspocketedge", str(self.root / "config"))
            script = script.replace("/etc/systemd/system", str(self.root / "units"))
            (self.root / "install-bin").mkdir(exist_ok=True)
            (self.root / "units").mkdir(exist_ok=True)
        copy = self.root / name
        copy.write_text(script)
        return subprocess.run(
            ["sh", str(copy), "--server=control.example:8443", "--token=test-token",
             f"--local-binary={self.agent}", *args],
            env=self.env, text=True, capture_output=True, timeout=10,
        )

    def assert_success(self, result):
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def mac_plist(self):
        return plistlib.loads((self.root / "home/Library/LaunchAgents/com.pspocketedge.agent.plist").read_bytes())

    def test_linux_docker_default(self):
        self.assert_success(self.run_installer())
        unit = (self.root / "units/pe-agent.service").read_text()
        self.assertIn("Requires=docker.service", unit)
        self.assertNotIn("podman.socket", unit)
        self.assertIn('container_runtime: "docker"', (self.root / "config/agent.yaml").read_text())
        self.assertNotIn("podman", self.calls.read_text())

    def test_linux_podman_service_and_config(self):
        self.assert_success(self.run_installer(args=("--runtime=podman", "--allow-builds")))
        config = self.root / "config/agent.yaml"
        self.assertIn('container_host: "unix:///run/podman/podman.sock"', config.read_text())
        self.assertIn("allow_builds: true", config.read_text())
        self.assertEqual(config.stat().st_mode & 0o777, 0o600)
        self.assertIn("Requires=podman.socket", (self.root / "units/pe-agent.service").read_text())
        self.assertIn("systemctl enable --now podman.socket", self.calls.read_text())
        self.assertIn("systemctl enable podman-restart.service", self.calls.read_text())

    def test_linux_podman_missing_fails_before_changing_services(self):
        (self.bin / "podman").unlink()
        if shutil.which("podman", path="/usr/bin:/bin"):
            self.skipTest("Podman is installed on this machine")
        result = self.run_installer(args=("--runtime=podman",))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Podman is not installed", result.stderr)
        self.assertFalse(self.calls.exists())

    def test_linux_explicit_host_overrides_environment(self):
        self.env["DOCKER_HOST"] = "unix:///tmp/other.sock"
        self.assert_success(self.run_installer(args=("--runtime=podman", "--container-host=unix:///tmp/selected.sock")))
        self.assertIn('container_host: "unix:///tmp/selected.sock"', (self.root / "config/agent.yaml").read_text())

    def test_linux_invalid_host_does_not_change_services(self):
        result = self.run_installer(args=("--runtime=podman", '--container-host=bad"host'))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("invalid container host", result.stderr)
        self.assertFalse(self.calls.exists())

    def test_invalid_runtime_is_rejected(self):
        for mac in (False, True):
            with self.subTest(mac=mac):
                result = self.run_installer(mac=mac, args=("--runtime=unsupported",))
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("--runtime must be docker or podman", result.stderr)
                self.assertFalse(self.calls.exists())

    def test_macos_podman_discovers_and_persists_socket(self):
        self.assert_success(self.run_installer(mac=True, args=("--runtime=podman",)))
        self.assertEqual(self.mac_plist()["EnvironmentVariables"]["DOCKER_HOST"], "unix:///tmp/podman-machine.sock")
        config = self.root / "home/Library/Application Support/PSpocketEdge/agent.yaml"
        self.assertIn('container_runtime: "podman"', config.read_text())
        self.assertIn("launchctl bootstrap gui/501", self.calls.read_text())

    def test_macos_custom_socket_and_xml_escaping(self):
        socket = "unix:///tmp/a & b.sock"
        self.assert_success(self.run_installer(mac=True, args=("--runtime=podman", f"--container-host={socket}")))
        self.assertEqual(self.mac_plist()["EnvironmentVariables"]["DOCKER_HOST"], socket)

    def test_macos_docker_default(self):
        self.assert_success(self.run_installer(mac=True))
        self.assertEqual(self.mac_plist()["EnvironmentVariables"]["DOCKER_HOST"],
                         f"unix://{self.root}/home/.colima/default/docker.sock")

    def test_macos_unreachable_socket_fails_before_installing(self):
        self.env["TEST_PING_FAIL"] = "1"
        result = self.run_installer(mac=True, args=("--runtime=podman",))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("cannot connect", result.stderr)
        self.assertFalse(self.calls.exists())

    def test_macos_ambiguous_or_empty_socket_has_actionable_error(self):
        for socket in ("", "/tmp/one.sock\n/tmp/two.sock", "<nil>"):
            with self.subTest(socket=socket):
                self.env["TEST_SOCKET"] = socket
                result = self.run_installer(mac=True, args=("--runtime=podman",))
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("Podman machine socket", result.stderr)
                self.assertFalse(self.calls.exists())


if __name__ == "__main__":
    unittest.main()
