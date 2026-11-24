"""Exercise the curl | sh path with fake releases and intercepted root commands."""

import hashlib
import io
import json
import os
from pathlib import Path
import pty
import select
import shutil
import subprocess
import tarfile
import tempfile
import time
import unittest


ROOT = Path(__file__).resolve().parents[1]

# No privileged command from the installer may escape this replacement.
ROOT_COMMANDS = r'''
as_root() {
    printf '%s\n' "$*" >> "$INSTALL_TEST_DIR/root-commands"
    case "$1" in
        install) return 0 ;;
        mktemp) mktemp "$INSTALL_TEST_DIR/staged-XXXXXXXX" ;;
        mv) return 0 ;;
        rm) shift; rm "$@" ;;
        /usr/local/bin/pifi) sh "$work_dir/pifi" setup ;;
        *) echo "Unexpected privileged command" >&2; exit 99 ;;
    esac
}
main "$@"
'''

MOCK_COMMAND = r'''#!/usr/bin/env python3
import json
import os
from pathlib import Path
import shutil
import sys

directory = Path(os.environ["INSTALL_TEST_DIR"])
name = Path(sys.argv[0]).name
if name == "uname":
    print("Linux" if sys.argv[1] == "-s" else os.environ.get("TEST_MACHINE", "aarch64"))
elif name == "getconf":
    print(os.environ.get("TEST_BITS", "64"))
elif name == "id":
    print("1000")
elif name == "nmcli":
    print("wifi")
elif name == "systemctl":
    sys.exit(0)
elif name == "sudo":
    sys.exit("Test unexpectedly attempted sudo")
elif name == "curl":
    url = sys.argv[-1]
    with (directory / "downloads").open("a") as log:
        log.write(url + "\n")
    if url.endswith("/latest"):
        source = directory / "release.json"
    else:
        if os.environ.get("TEST_DOWNLOAD_FAIL"):
            sys.exit(22)
        source = directory / "archive.tar.gz"
    shutil.copyfile(source, sys.argv[sys.argv.index("--output") + 1])
else:
    sys.exit("Unexpected mock command: " + name)
'''


class InstallerTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.bin = self.directory / "bin"
        self.bin.mkdir()
        for name in ("uname", "getconf", "id", "nmcli", "systemctl", "sudo", "curl"):
            command = self.bin / name
            command.write_text(MOCK_COMMAND)
            command.chmod(0o755)
        source = (ROOT / "install.sh").read_text()
        self.assertTrue(source.endswith('main "$@"\n'))
        self.script = self.directory / "install.sh"
        self.script.write_text(source.removesuffix('main "$@"\n') + ROOT_COMMANDS)
        self.make_archive()
        self.env = dict(os.environ, INSTALL_TEST_DIR=str(self.directory),
                        TMPDIR=str(self.directory), PATH=str(self.bin) + ":" + os.environ["PATH"])

    def make_archive(self, symlink=False):
        archive = self.directory / "archive.tar.gz"
        with tarfile.open(archive, "w:gz") as output:
            binary = tarfile.TarInfo("pifi")
            if symlink:
                binary.type = tarfile.SYMTYPE
                binary.linkname = "/untrusted"
                output.addfile(binary)
            else:
                content = b'#!/bin/sh\n[ "$1" = setup ] && [ -t 0 ] || exit 88\necho setup-read-terminal\n'
                binary.size = len(content)
                output.addfile(binary, io.BytesIO(content))
        digest = "sha256:" + hashlib.sha256(archive.read_bytes()).hexdigest()
        self.release = {"tag_name": "v1.2.3", "draft": False, "prerelease": False, "assets": []}
        for arch in ("armv6", "arm64"):
            name = f"pifi-v1.2.3-linux-{arch}.tar.gz"
            self.release["assets"].append({
                "name": name, "state": "uploaded", "digest": digest,
                "browser_download_url": f"https://github.com/stut/pifi/releases/download/v1.2.3/{name}",
            })

    def run_installer(self, terminal=True, shell="/bin/sh", **environment):
        (self.directory / "release.json").write_text(json.dumps(self.release))
        env = dict(self.env, **environment)
        command = [shell, "-c", 'cat "$1" | "$2"', "test-installer", str(self.script), shell]
        if not terminal:
            result = subprocess.run(command, env=env, capture_output=True, text=True,
                                    start_new_session=True, timeout=10)
            return result.returncode, result.stdout + result.stderr
        # Give the pipeline a controlling terminal while its stdin is the script.
        pid, fd = pty.fork()
        if pid == 0:
            os.execvpe(command[0], command, env)
        output = bytearray()
        try:
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                if select.select([fd], [], [], 0.1)[0]:
                    try:
                        chunk = os.read(fd, 65536)
                    except OSError:
                        break
                    if not chunk:
                        break
                    output.extend(chunk)
            else:
                os.kill(pid, 9)
                self.fail("installer timed out")
        finally:
            os.close(fd)
            _, status = os.waitpid(pid, 0)
        return os.waitstatus_to_exitcode(status), output.decode()

    def root_commands(self):
        log = self.directory / "root-commands"
        return log.read_text() if log.exists() else ""

    def assert_rejected(self, message, **environment):
        code, output = self.run_installer(**environment)
        self.assertNotEqual(code, 0, output)
        self.assertIn(message, output)
        self.assertEqual(self.root_commands(), "", "failure must precede any privileged operation")

    def test_architecture_and_terminal_setup(self):
        for machine, bits, asset in (("armv6l", "32", "armv6"), ("armv7l", "32", "armv6"),
                                     ("aarch64", "32", "armv6"), ("aarch64", "64", "arm64")):
            with self.subTest(machine=machine, bits=bits):
                code, output = self.run_installer(TEST_MACHINE=machine, TEST_BITS=bits)
                self.assertEqual(code, 0, output)
                self.assertIn("setup-read-terminal", output)
                self.assertIn(f"linux-{asset}.tar.gz", (self.directory / "downloads").read_text().splitlines()[-1])
                commands = self.root_commands()
                self.assertIn("mv -fT --", commands)
                self.assertIn("/usr/local/bin/pifi setup", commands)
                self.assertNotIn("systemctl restart", commands)

    def test_unsupported_architecture(self):
        self.assert_rejected("Unsupported architecture", TEST_MACHINE="x86_64")

    def test_requires_terminal(self):
        # macOS sh is bash; Ubuntu sh is dash. Both must print the diagnostic
        # rather than silently exit when opening /dev/tty fails.
        for shell in dict.fromkeys(("/bin/sh", shutil.which("dash") or "/bin/sh")):
            with self.subTest(shell=shell):
                self.assert_rejected("interactive terminal", terminal=False, shell=shell)

    def test_dash_terminal_setup(self):
        shell = shutil.which("dash")
        if not shell:
            self.skipTest("dash is not installed")
        code, output = self.run_installer(shell=shell)
        self.assertEqual(code, 0, output)
        self.assertIn("setup-read-terminal", output)

    def test_missing_asset(self):
        self.release["assets"] = []
        self.assert_rejected("build may still be running")

    def test_missing_digest(self):
        for asset in self.release["assets"]:
            asset.pop("digest")
        self.assert_rejected("no SHA-256 digest")

    def test_checksum_mismatch(self):
        for asset in self.release["assets"]:
            asset["digest"] = "sha256:" + "0" * 64
        self.assert_rejected("SHA-256 verification failed")

    def test_unexpected_download_url(self):
        for asset in self.release["assets"]:
            asset["browser_download_url"] = "https://untrusted.example/pifi.tar.gz"
        self.assert_rejected("Unexpected release download URL")

    def test_archive_symlink(self):
        self.make_archive(symlink=True)
        self.assert_rejected("one regular pifi binary")

    def test_failed_download(self):
        code, output = self.run_installer(TEST_DOWNLOAD_FAIL="1")
        self.assertNotEqual(code, 0, output)
        self.assertEqual(self.root_commands(), "")


if __name__ == "__main__":
    unittest.main()
