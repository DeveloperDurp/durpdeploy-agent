#!/usr/bin/env python3
"""Bind runtime verification and publication to the same multi-platform OCI bytes."""

import hashlib
import json
import os
from pathlib import Path
import re
import sys


def describe():
    # These private CI artifacts are fixed; CLI arguments may never select files.
    root = Path(".ci/agent-candidate")
    for path in [root, root / "blobs", root / "blobs/sha256", root / "index.json", root / "oci-layout"]:
        if path.is_symlink():
            raise ValueError("linked OCI layout path")
    seen = set()

    def blob(descriptor):
        digest = descriptor["digest"]
        if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
            raise ValueError("unsupported OCI digest")
        path = root / "blobs" / "sha256" / digest.removeprefix("sha256:")
        if path.is_symlink() or not path.is_file():
            raise ValueError("missing or linked OCI blob")
        if path.stat().st_size != descriptor["size"]:
            raise ValueError("OCI blob size mismatch")
        if digest not in seen:
            with path.open("rb") as stream:
                actual = "sha256:" + hashlib.file_digest(stream, "sha256").hexdigest()
            if actual != digest:
                raise ValueError("OCI blob digest mismatch")
            seen.add(digest)
        return path

    def document(descriptor):
        if descriptor["size"] > 4 * 1024 * 1024:
            raise ValueError("oversized OCI metadata")
        return json.loads(blob(descriptor).read_bytes())

    if json.loads((root / "oci-layout").read_text())["imageLayoutVersion"] != "1.0.0":
        raise ValueError("unsupported OCI layout")
    index = json.loads((root / "index.json").read_text())
    if index["schemaVersion"] != 2 or len(index["manifests"]) != 1:
        raise ValueError("require a single multi-platform candidate index")
    candidate = index["manifests"][0]
    manifest = document(candidate)
    if manifest["schemaVersion"] != 2 or "manifests" not in manifest:
        raise ValueError("candidate must be a multi-platform index")
    images = {}
    for descriptor in manifest["manifests"]:
        image = document(descriptor)
        config = document(image["config"])
        for layer in image["layers"]:
            blob(layer)
        platform = descriptor.get("platform", {})
        architecture = platform.get("architecture")
        if platform.get("os") == "unknown" and architecture == "unknown":
            # BuildKit provenance is retained in the copied index.
            if descriptor.get("annotations", {}).get("vnd.docker.reference.type") != "attestation-manifest":
                raise ValueError("unexpected unknown-platform image")
            continue
        if platform.get("os") != "linux" or architecture not in {"amd64", "arm64"}:
            raise ValueError("unexpected candidate platform")
        if architecture in images or config["os"] != "linux" or config["architecture"] != architecture:
            raise ValueError("duplicate or mismatched candidate platform")
        images[architecture] = {
            "manifest": descriptor["digest"],
            "config": image["config"]["digest"],
            "labels": config["config"].get("Labels", {}),
        }
    if images.keys() != {"amd64", "arm64"}:
        raise ValueError("candidate must retain both publication architectures")
    return {"schema": 1, "index": candidate["digest"], "images": images}


def main():
    mode, *arguments = sys.argv[1:]
    result = describe()
    if mode == "verify-runtime":
        if arguments != [result["images"]["amd64"]["config"]]:
            raise ValueError("runtime image differs from the built amd64 candidate")
    elif mode == "verify-proof":
        if arguments or json.loads(Path(".ci/image-verified.json").read_text()) != result:
            raise ValueError("candidate differs from the runtime verification proof")
        expected = {
            "org.opencontainers.image.revision": os.environ["BUILD_COMMIT"],
            "org.opencontainers.image.version": os.environ["BUILD_VERSION"],
            "info.durpdeploy.build-number": os.environ["BUILD_NUMBER"],
        }
        if os.environ["BUILD_COMMIT"] != os.environ["CI_COMMIT_SHA"]:
            raise ValueError("candidate commit differs from the publishing commit")
        for image in result["images"].values():
            if any(image["labels"].get(key) != value for key, value in expected.items()):
                raise ValueError("candidate build identity differs from publication metadata")
        print(result["index"])
        return
    elif mode != "describe" or arguments:
        raise ValueError("invalid image-artifact invocation")
    print(json.dumps(result, sort_keys=True, indent=2))


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, OSError, TypeError) as error:
        sys.exit(f"image artifact: {error}")
