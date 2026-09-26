package codex

// Version pins the CLI release and its bundled App Server schema. It is also the
// AR compatibility identifier for this snapshot, not an upstream wire SemVer.
const Version = "0.155.0"

// LinuxAMD64SHA256 identifies the exact executable used by the local CDX-001
// protocol probe. This is not an OCI digest or ARM64 qualification.
const LinuxAMD64SHA256 = "660e159a49e823ac8e5986cb238f73158ce4b957d40d9292f8de90862644b501"

// SchemaSHA256 pins the stable (non-experimental) JSON schema export. The probe
// hashes sorted relative paths, a NUL separator, and each file's original bytes.
const SchemaSHA256 = "5b0fbb54807f53f2286a0aab3428cd6893ef7cdfc0efa0f3151450d70a80ddd6"

// LinuxARM64SHA256 pins the executable from the checksum-verified 0.155.0
// aarch64 musl archive packaged by Dockerfile.codex.
const LinuxARM64SHA256 = "98a3ca0f4edf0e6afccf73cba2f129dc71c7992638f94a78a36f34fe01a019d7"
