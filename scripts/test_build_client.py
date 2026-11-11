import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("build_client", Path(__file__).with_name("build-client.py"))
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)


class SigningConfigurationTest(unittest.TestCase):
    def test_configured_identity_survives_final_seal(self):
        config = {"macos": {"signingIdentity": "Synthetic signing identity"}}
        identity = builder.signing_identity(config)
        command = builder.seal_command("/tmp/Synthetic.app", identity)
        self.assertEqual(command[command.index("--sign") + 1], config["macos"]["signingIdentity"])
        self.assertIn("--timestamp", command)
        self.assertIn("runtime", command)
        self.assertNotIn("--deep", command)
        self.assertIn("--preserve-metadata=entitlements", command)

    def test_explicit_development_override_and_entitlements(self):
        identity = builder.signing_identity({"macos": {"signingIdentity": "Synthetic"}}, "-")
        command = builder.seal_command("/tmp/Synthetic.app", identity, "/tmp/entitlements.plist")
        self.assertIn("--timestamp=none", command)
        self.assertNotIn("runtime", command)
        self.assertIn("/tmp/entitlements.plist", command)
        self.assertNotIn("--preserve-metadata=entitlements", command)

    def test_empty_signer_does_not_silently_downgrade(self):
        with self.assertRaises(ValueError):
            builder.signing_identity({"macos": {"signingIdentity": ""}})


if __name__ == "__main__":
    unittest.main()
