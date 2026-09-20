"""Reject runtime branding without exact sandbox identity and KVM selection."""
import importlib.util
import pathlib
import unittest

spec = importlib.util.spec_from_file_location("proof", pathlib.Path(__file__).with_name("kata-host-proof.py"))
proof = importlib.util.module_from_spec(spec)
spec.loader.exec_module(proof)


class RuntimeProofTests(unittest.TestCase):
    def test_rejects_wrong_sandbox_and_emulation(self):
        args = ["qemu", "-name", "sandbox-abc", "-machine", "virt,accel=kvm",
                "-kernel", "/kernel", "-blockdev", "driver=file,filename=/image"]
        self.assertEqual(proof.inspect_qemu(args, "abc"), {"kernel": "/kernel", "image": "/image"})
        with self.assertRaises(RuntimeError):
            proof.inspect_qemu(args, "other")
        args[4] = "virt,accel=tcg"
        with self.assertRaises(RuntimeError):
            proof.inspect_qemu(args, "abc")

    def test_rejects_ambiguous_artifacts(self):
        args = ["qemu", "-name", "sandbox-abc", "-machine", "virt,accel=kvm",
                "-kernel", "/kernel", "-blockdev", "filename=/image",
                "-blockdev", "filename=/extra"]
        with self.assertRaises(RuntimeError):
            proof.inspect_qemu(args, "abc")


if __name__ == "__main__":
    unittest.main()
