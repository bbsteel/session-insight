#!/usr/bin/env python3
"""Exercise pkg-config filtering without requiring system development packages."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


class PkgConfigWrapperTests(unittest.TestCase):
    def test_wrapper_queries(self):
        temporary_root = Path.home() / "tmp"
        temporary_root.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=temporary_root) as fixture_directory:
            fake_pkg_config = Path(fixture_directory) / "pkg-config"
            fake_pkg_config.write_text(
                "#!/usr/bin/env bash\n"
                "if [[ $1 == --exists ]]; then exit 0; fi\n"
                "if [[ ${FAKE_PKG_CONFIG_STATUS:-0} != 0 ]]; then\n"
                "  printf '%s\\n' \"Package 'libsystemd' not found; check \\`libsystemd.pc'\" >&2\n"
                "  exit \"$FAKE_PKG_CONFIG_STATUS\"\n"
                "fi\n"
                "printf '%s\\n' \"$FAKE_PKG_CONFIG_OUTPUT\"\n",
                encoding="utf-8",
            )
            fake_pkg_config.chmod(0o755)
            wrapper_path = Path(__file__).with_name("pkg-config")
            for query in ("--cflags", "--libs"):
                with self.subTest(query=query, outcome="missing dependency"):
                    query_environment = dict(
                        os.environ,
                        REAL_PKG_CONFIG=str(fake_pkg_config),
                        FAKE_PKG_CONFIG_STATUS="23",
                    )
                    result = subprocess.run(
                        [str(wrapper_path), query, "--", "gtk+-3.0"],
                        env=query_environment, capture_output=True, text=True,
                    )
                    self.assertEqual(result.returncode, 23)
                    self.assertEqual(result.stdout, "")
                    self.assertIn("`libsystemd.pc'", result.stderr)

            for query, package, original_flags, expected_flags in (
                ("--cflags", "gtk+-3.0", "-I/usr/include -I/gtk -pthread",
                 "-idirafter/usr/include -I/gtk -pthread -idirafter/usr/include\n"),
                ("--libs", "gtk+-3.0", "-L/usr/lib -lgtk-3 -pthread", "-pthread\n"),
                ("--libs", "sqlite3", "-L/usr/lib -lsqlite3", "-L/usr/lib -lsqlite3\n"),
            ):
                with self.subTest(query=query, package=package, outcome="success"):
                    query_environment = dict(
                        os.environ,
                        REAL_PKG_CONFIG=str(fake_pkg_config),
                        FAKE_PKG_CONFIG_STATUS="0",
                        FAKE_PKG_CONFIG_OUTPUT=original_flags,
                    )
                    query_environment.pop("PKG_CONFIG_SYSROOT_DIR", None)
                    result = subprocess.run(
                        [str(wrapper_path), query, "--", package],
                        env=query_environment, capture_output=True, text=True,
                    )
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(result.stdout, expected_flags)
                    self.assertEqual(result.stderr, "")


if __name__ == "__main__":
    unittest.main()
