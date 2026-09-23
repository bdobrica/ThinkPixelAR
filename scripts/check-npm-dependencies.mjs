import fs from "node:fs";

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
  if (typeof dependency.license !== "string" ||
      !dependency.license.trim() ||
      /^(UNKNOWN|NOASSERTION|NONE)$/i.test(dependency.license.trim())) {
    failures.push(`${name}@${dependency.version}: missing or unknown license ${dependency.license ?? "unknown"}`);
  }
  inventory.push(`${name}\t${dependency.version}\t${dependency.license}`);
}

if (failures.length) {
  throw new Error(`dependency policy:\n${failures.join("\n")}`);
}

console.log("npm build dependency inventory:");
console.log(inventory.sort().join("\n"));
