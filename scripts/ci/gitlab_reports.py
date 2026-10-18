#!/usr/bin/env python3
"""Prepare existing test reports for GitLab's test summary and diff annotations."""

from pathlib import Path, PurePosixPath
import xml.etree.ElementTree as ET


def main():
    for report in Path(".ci/junit").glob("*.xml"):
        tree = ET.parse(report)
        prefix = report.stem + ":"
        # The same test can run with race detection and again with coverage.
        # GitLab otherwise deduplicates their results, potentially hiding a failure.
        for element in tree.iter():
            attribute = {"testsuite": "name", "testcase": "classname"}.get(element.tag)
            if attribute:
                value = element.get(attribute, "")
                if not value.startswith(prefix):
                    element.set(attribute, prefix + value)
        tree.write(report, encoding="utf-8", xml_declaration=True)

    coverage = Path(".ci/coverage-python.xml")
    if coverage.exists():
        tree = ET.parse(coverage)
        for element in tree.findall(".//class"):
            path = PurePosixPath(element.attrib["filename"])
            if path.is_absolute() or ".." in path.parts:
                raise ValueError("coverage filename must stay inside the repository")
            if not path.is_relative_to("scripts/ci"):
                path = PurePosixPath("scripts/ci") / path
            element.set("filename", str(path))
        for source in tree.findall("./sources/source"):
            source.text = "."
        tree.write(".ci/coverage-python-gitlab.xml", encoding="utf-8", xml_declaration=True)


if __name__ == "__main__":
    main()
