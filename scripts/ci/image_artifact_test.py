#!/usr/bin/env python3
"""Exercise corruption, stale-proof and platform rejection at the release boundary."""

import hashlib
import contextlib
import io
import json
import os
from pathlib import Path
import runpy
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock


CHECKER = Path(__file__).with_name("image-artifact.py").resolve()


class ImageArtifactTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name) / ".ci/agent-candidate"
        (self.root / "blobs/sha256").mkdir(parents=True)
        (self.root / "oci-layout").write_text('{"imageLayoutVersion":"1.0.0"}')
        self.environment = dict(os.environ, BUILD_COMMIT="a" * 40, CI_COMMIT_SHA="a" * 40,
                                BUILD_VERSION="v1.0.0", BUILD_NUMBER="1")
        self.manifests = []
        for arch in ["amd64", "arm64"]:
            config = self.blob({"architecture": arch, "os": "linux", "config": {"Labels": {
                "org.opencontainers.image.revision": "a" * 40,
                "org.opencontainers.image.version": "v1.0.0", "info.durpdeploy.build-number": "1"}}})
            layer = self.blob({"test-layer": arch})
            image = self.blob({"schemaVersion": 2, "config": config, "layers": [layer]})
            image["platform"] = {"os": "linux", "architecture": arch}
            self.manifests.append(image)
        self.index()

    def blob(self, value):
        data = json.dumps(value).encode()
        digest = hashlib.sha256(data).hexdigest()
        (self.root / "blobs/sha256" / digest).write_bytes(data)
        return {"digest": "sha256:" + digest, "size": len(data)}

    def index(self):
        descriptor = self.blob({"schemaVersion": 2, "manifests": self.manifests})
        (self.root / "index.json").write_text(json.dumps({"schemaVersion": 2, "manifests": [descriptor]}))

    def run_checker(self, mode="describe", *arguments, environment=None):
        output, errors = io.StringIO(), io.StringIO()
        status = 0
        with contextlib.chdir(self.temporary.name), \
                mock.patch.object(sys, "argv", [str(CHECKER), mode, *arguments]), \
                mock.patch.dict(os.environ, environment or self.environment, clear=True), \
                contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
            try:
                runpy.run_path(str(CHECKER), run_name="__main__")
            except SystemExit as error:
                status = 1
                errors.write(str(error))
        return SimpleNamespace(returncode=status, stdout=output.getvalue(), stderr=errors.getvalue())

    def test_verified_runtime_and_publication_use_the_same_candidate(self):
        result = self.run_checker()
        self.assertEqual(result.returncode, 0, result.stderr)
        proof = json.loads(result.stdout)
        runtime = self.run_checker("verify-runtime", proof["images"]["amd64"]["config"])
        self.assertEqual(runtime.returncode, 0, runtime.stderr)
        path = self.root.parent / "image-verified.json"
        path.write_text(runtime.stdout)
        publication = self.run_checker("verify-proof")
        self.assertEqual(publication.returncode, 0, publication.stderr)
        self.assertEqual(publication.stdout.strip(), proof["index"])

    def test_rejects_a_different_runtime_image(self):
        self.assertNotEqual(self.run_checker("verify-runtime", "sha256:" + "b" * 64).returncode, 0)

    def test_rejects_changed_layer_bytes(self):
        descriptor = self.manifests[0]
        image = json.loads((self.root / "blobs/sha256" / descriptor["digest"][7:]).read_text())
        layer = self.root / "blobs/sha256" / image["layers"][0]["digest"][7:]
        layer.write_bytes(b"x" * layer.stat().st_size)
        result = self.run_checker()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("digest mismatch", result.stderr)

    def test_rejects_stale_proof_and_wrong_publication_commit(self):
        path = self.root.parent / "image-verified.json"
        path.write_text(self.run_checker().stdout)
        wrong = dict(self.environment, CI_COMMIT_SHA="b" * 40)
        self.assertNotEqual(self.run_checker("verify-proof", environment=wrong).returncode, 0)
        self.manifests.reverse()
        self.index()
        self.assertNotEqual(self.run_checker("verify-proof").returncode, 0)

    def test_rejects_missing_or_duplicate_publication_architecture(self):
        self.manifests.pop()
        self.index()
        self.assertNotEqual(self.run_checker().returncode, 0)
        self.manifests.append(self.manifests[0])
        self.index()
        self.assertNotEqual(self.run_checker().returncode, 0)

    def test_rejects_linked_layout_and_blob_paths(self):
        path = self.root / "index.json"
        saved = path.read_bytes()
        other = self.root / "other.json"
        other.write_bytes(saved)
        path.unlink()
        path.symlink_to(other)
        self.assertNotEqual(self.run_checker().returncode, 0)
        path.unlink()
        path.write_bytes(saved)
        descriptor = self.manifests[0]
        blob = self.root / "blobs/sha256" / descriptor["digest"][7:]
        other.write_bytes(blob.read_bytes())
        blob.unlink()
        blob.symlink_to(other)
        self.assertNotEqual(self.run_checker().returncode, 0)

    def test_rejects_corrupt_or_unsafe_metadata_descriptors(self):
        path = self.root / "index.json"
        original = json.loads(path.read_text())
        for field, value in [("digest", "sha256:../../escape"), ("size", 1), ("size", 4 * 1024 * 1024 + 1)]:
            with self.subTest(field=field, value=value):
                index = json.loads(json.dumps(original))
                index["manifests"][0][field] = value
                path.write_text(json.dumps(index))
                self.assertNotEqual(self.run_checker().returncode, 0)

    def test_rejects_unsupported_layouts_and_platforms(self):
        layout = self.root / "oci-layout"
        layout.write_text('{"imageLayoutVersion":"2.0.0"}')
        self.assertNotEqual(self.run_checker().returncode, 0)
        layout.write_text('{"imageLayoutVersion":"1.0.0"}')
        index = self.root / "index.json"
        data = json.loads(index.read_text())
        data["schemaVersion"] = 3
        index.write_text(json.dumps(data))
        self.assertNotEqual(self.run_checker().returncode, 0)
        index.write_text(json.dumps({"schemaVersion": 2, "manifests": [self.manifests[0]]}))
        self.assertNotEqual(self.run_checker().returncode, 0)
        self.manifests[1]["platform"]["architecture"] = "s390x"
        self.index()
        self.assertNotEqual(self.run_checker().returncode, 0)

    def test_preserves_attestations_and_rejects_disguised_platforms(self):
        config = self.blob({"architecture": "unknown", "os": "unknown", "config": {}})
        attestation = self.blob({"schemaVersion": 2, "config": config, "layers": []})
        attestation["platform"] = {"os": "unknown", "architecture": "unknown"}
        self.manifests.append(attestation)
        self.index()
        self.assertNotEqual(self.run_checker().returncode, 0)
        attestation["annotations"] = {"vnd.docker.reference.type": "attestation-manifest"}
        self.index()
        self.assertEqual(self.run_checker().returncode, 0)

    def test_rejects_wrong_release_identity_and_invocation(self):
        path = self.root.parent / "image-verified.json"
        path.write_text(self.run_checker().stdout)
        wrong = dict(self.environment, BUILD_VERSION="v2.0.0")
        self.assertNotEqual(self.run_checker("verify-proof", environment=wrong).returncode, 0)
        self.assertNotEqual(self.run_checker("invalid-mode").returncode, 0)

    def test_rejects_cli_artifact_path_overrides(self):
        path = self.root.parent / "image-verified.json"
        path.write_text(self.run_checker().stdout)
        for mode in ["describe", "verify-runtime", "verify-proof"]:
            with self.subTest(mode=mode):
                result = self.run_checker(mode, "../../outside-ci")
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn("No such file", result.stderr)


if __name__ == "__main__":
    unittest.main()
