import fs from "node:fs";

const allowedLicenses = new Set([
  "Apache-2.0",
  "BSD-2-Clause",
  "BSD-3-Clause",
  "CC0-1.0",
  "ISC",
  "MIT",
  "Unlicense",
]);
const lock = JSON.parse(fs.readFileSync("package-lock.json", "utf8"));

if (lock.lockfileVersion !== 3 || !lock.packages) {
  throw new Error("dependency policy: package-lock.json must use lockfileVersion 3");
}

const failures = [];
const inventory = [];
for (const [path, dependency] of Object.entries(lock.packages)) {
  if (path === "") continue;
  const name = path.slice(path.lastIndexOf("node_modules/") + "node_modules/".length);
  if (!dependency.version || !dependency.integrity) {
    failures.push(`${name}: missing exact version or integrity`);
  }
  if (!allowedLicenses.has(dependency.license)) {
    failures.push(`${name}@${dependency.version}: unapproved license ${dependency.license ?? "unknown"}`);
  }
  inventory.push(`${name}\t${dependency.version}\t${dependency.license}`);
}

if (failures.length) {
  throw new Error(`dependency policy:\n${failures.join("\n")}`);
}

console.log("npm build dependency inventory:");
console.log(inventory.sort().join("\n"));
