#!/usr/bin/env python3
"""Exercise GitLab report identity, source mapping and invalid report failures."""

import contextlib
from pathlib import Path
import runpy
import tempfile
import unittest
import xml.etree.ElementTree as ET


REPORTER = Path(__file__).with_name("gitlab_reports.py").resolve()


class GitLabReportsTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        (self.root / ".ci/junit").mkdir(parents=True)

    def run_reporter(self):
        with contextlib.chdir(self.root):
            runpy.run_path(str(REPORTER), run_name="__main__")

    def test_repeated_tests_keep_distinct_failures_and_stable_names(self):
        for name, result in [("race", ""), ("go", '<failure>broken &amp; failed</failure>')]:
            (self.root / f".ci/junit/{name}.xml").write_text(
                '<testsuites><testsuite name="pkg"><testcase classname="pkg" name="TestOne">'
                + result + '</testcase></testsuite></testsuites>')
        self.run_reporter()
        self.run_reporter()
        cases = []
        for path in (self.root / ".ci/junit").glob("*.xml"):
            tree = ET.parse(path)
            self.assertEqual(tree.find("testsuite").get("name"), path.stem + ":pkg")
            cases.append(tree.find(".//testcase"))
        self.assertEqual({case.get("classname") for case in cases}, {"race:pkg", "go:pkg"})
        self.assertEqual(sum(case.find("failure") is not None for case in cases), 1)

    def test_python_paths_resolve_from_repository_root_without_changing_sonar_input(self):
        source = self.root / ".ci/coverage-python.xml"
        raw = ('<coverage><sources><source>scripts/ci</source></sources><packages><package><classes>'
               '<class filename="image-artifact.py"><lines><line number="1" hits="1"/></lines></class>'
               '<class filename="scripts/ci/gitlab_reports.py"/>'
               '</classes></package></packages></coverage>')
        source.write_text(raw)
        self.run_reporter()
        self.assertEqual(source.read_text(), raw)
        tree = ET.parse(self.root / ".ci/coverage-python-gitlab.xml")
        self.assertEqual(tree.find("./sources/source").text, ".")
        self.assertEqual([node.get("filename") for node in tree.findall(".//class")],
                         ["scripts/ci/image-artifact.py", "scripts/ci/gitlab_reports.py"])
        self.assertEqual(tree.find(".//line").attrib, {"number": "1", "hits": "1"})

    def test_invalid_xml_and_escaping_coverage_paths_fail(self):
        report = self.root / ".ci/junit/go.xml"
        report.write_text("<invalid")
        with self.assertRaises(ET.ParseError):
            self.run_reporter()
        report.unlink()
        for filename in ["../outside.py", "/outside.py"]:
            with self.subTest(filename=filename):
                (self.root / ".ci/coverage-python.xml").write_text(
                    f'<coverage><class filename="{filename}"/></coverage>')
                with self.assertRaises(ValueError):
                    self.run_reporter()


if __name__ == "__main__":
    unittest.main()
