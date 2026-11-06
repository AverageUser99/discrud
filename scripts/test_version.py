"""Offline regression tests for automatic release version selection."""
import unittest
from unittest.mock import patch

from version import choose_version, parse_tag, resolve_version


class VersionTests(unittest.TestCase):
    def test_next_patch(self):
        self.assertEqual(choose_version(["v0.1.2"], []), "0.1.3")

    def test_numeric_order(self):
        self.assertEqual(choose_version(["v0.9.9", "v0.10.2", "v0.10.10"], []), "0.10.11")

    def test_tagged_rebuild(self):
        self.assertEqual(choose_version(["v0.1.2", "v0.2.0"], ["v0.1.2"]), "0.1.2")

    def test_multiple_head_tags(self):
        self.assertEqual(choose_version([], ["v1.0.0", "v1.1.0"]), "1.1.0")

    def test_initial_version(self):
        self.assertEqual(choose_version([], []), "0.1.0")

    def test_ignore_nonstable_tags(self):
        tags = ["v0.1.2", "v99.0.0-rc.1", "v01.2.3", "1.2.3", "v2.3.4junk"]
        self.assertEqual(choose_version(tags, []), "0.1.3")
        self.assertIsNone(parse_tag("v1.2.3\n"))

    @patch("version.subprocess.check_output")
    def test_git_integration(self, git):
        git.side_effect = ["v0.1.2\nv0.1.3\n", ""]
        self.assertEqual(resolve_version(), "0.1.4")
        self.assertEqual(git.call_args_list[1].args[0], ["git", "tag", "--points-at", "HEAD"])

    @patch("version.subprocess.check_output", side_effect=RuntimeError("git failed"))
    def test_git_errors_are_not_hidden(self, git):
        with self.assertRaises(RuntimeError):
            resolve_version()


if __name__ == "__main__":
    unittest.main()
