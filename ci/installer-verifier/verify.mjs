// The contract test: the service installer's own minisign verifier (vendored byte-for-byte
// under src/, see README.md), with its verifyRelease rules applied to @vbcdx/keyhole, must
// accept what `keyhole-release pack` + `sign` produce, and must refuse the same tree under
// another key or with one byte of a binary changed.
//   node verify.mjs <packed root> <public key line> <version>
import { cpSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { verifyMinisign } from "./src/service/minisign.js";
import { conflictError } from "./src/errors.js";

const [root, publicKey, version] = process.argv.slice(2);
if (!root || !publicKey || !version) {
  console.error("usage: node verify.mjs <packed root> <public key line> <version>");
  process.exit(2);
}
const PACKAGE = "@vbcdx/keyhole";
const BINARIES = ["keyhole"];
const PLATFORMS = ["linux-x64-gnu", "linux-x64-musl"];
// The installer's SEMVER (src/service/artifact.js at the vendored commit).
const ID = "(?:0|[1-9]\\d*|\\d*[A-Za-z-][0-9A-Za-z-]*)";
const SEMVER = new RegExp(
  `^(?:0|[1-9]\\d*)\\.(?:0|[1-9]\\d*)\\.(?:0|[1-9]\\d*)(?:-${ID}(?:\\.${ID})*)?(?:\\+[0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*)?$`);

// verifyRelease's steps, in its order, for PACKAGE: the signature first, then the schema,
// package, version and trusted comment, then each binary's SHA-256.
function check(dir, trustedKeys, platformId) {
  const raw = readFileSync(join(dir, "checksums.json"));
  const signature = verifyMinisign(raw, readFileSync(join(dir, "checksums.json.minisig"), "utf8"), trustedKeys);
  const sums = JSON.parse(raw.toString("utf8"));
  if (!sums || sums.schema !== 1 || typeof sums.files !== "object" || sums.files === null) throw conflictError("checksums.json is not schema 1.");
  if (sums.package !== PACKAGE) throw conflictError(`The release is ${sums.package}, not ${PACKAGE}.`);
  if (typeof sums.version !== "string" || !SEMVER.test(sums.version)) throw conflictError("checksums.json's version is not a semantic version.");
  if (signature.trustedComment !== `vbcdx-release ${sums.package} ${sums.version}`) {
    throw conflictError("The signature's trusted comment does not name this package and version.");
  }
  if (sums.version !== version) throw conflictError(`The release is version ${sums.version}, not ${version}.`);
  const binaries = BINARIES.map((name) => {
    const key = `${PACKAGE}-${platformId}/bin/${name}`;
    const want = sums.files[key];
    if (typeof want !== "string" || !/^[0-9a-f]{64}$/.test(want)) throw conflictError(`The release has no checksum for ${key}.`);
    const path = join(dir, key);
    if (!statSync(path).isFile()) throw conflictError(`The release is missing ${key}.`);
    const got = createHash("sha256").update(readFileSync(path)).digest("hex");
    if (got !== want) throw conflictError(`${key} does not match its signed checksum.`);
    return `${name} ${got}`;
  });
  return { signature, binaries };
}

let failed = false;
for (const platformId of PLATFORMS) {
  const r = check(root, [publicKey], platformId);
  console.log(`accepted ${platformId}: ${r.signature.trustedComment}; ${r.binaries.join(", ")}`);
}

// Each refusal must come from the verifier itself (exit 5), never from a missing file.
function refused(what, dir, keys) {
  try {
    check(dir, keys, PLATFORMS[0]);
    console.error(`NOT refused: ${what}`);
    failed = true;
  } catch (e) {
    if (e.code !== 5) {
      console.error(`refused for the wrong reason (${what}): ${e.message}`);
      failed = true;
    } else {
      console.log(`refused ${what}: ${e.message}`);
    }
  }
}
// A valid key that didn't sign it: the same shape as a real one, with another key id.
const other = Buffer.from(publicKey, "base64");
other[2] ^= 0xff;
refused("under another key", root, [other.toString("base64")]);
const copy = mkdtempSync(join(tmpdir(), "installer-verifier-"));
try {
  cpSync(root, copy, { recursive: true });
  const bin = join(copy, `${PACKAGE}-${PLATFORMS[0]}/bin/keyhole`);
  const bytes = readFileSync(bin);
  bytes[0] ^= 0x01;
  writeFileSync(bin, bytes);
  refused("with one byte of a binary changed", copy, [publicKey]);
} finally {
  rmSync(copy, { recursive: true, force: true });
}
process.exit(failed ? 1 : 0);
