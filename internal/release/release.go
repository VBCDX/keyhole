// Package release packs and signs the Keyhole server for npm, in the layout
// the vbcdx service installer verifies (the same contract as vbcdx-core's
// `cargo xtask pack-npm` / `sign` for @vbcdx/sidecar):
//
//	OUT/checksums.json(.minisig)                  what `--from OUT` verifies
//	OUT/@vbcdx/keyhole/                           the meta package (same checksums.json)
//	OUT/@vbcdx/keyhole-<platform>/bin/keyhole     one package per platform
//
// checksums.json is {"schema":1,"package","version","files"} with files keyed
// "@vbcdx/keyhole-<platform>/bin/keyhole", and its minisign signature is
// prehashed (Ed25519 over BLAKE2b-512) with the trusted comment exactly
// "vbcdx-release @vbcdx/keyhole <version>".
//
// Keyhole is one static binary (CGO_ENABLED=0), so the glibc and musl
// packages carry the same file: npm installs only the one whose `libc`
// matches the host, and the installer looks for that platform's name.
package release

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Package is the npm meta package; Binary is the one file each platform
// package carries.
const (
	Package = "@vbcdx/keyhole"
	Binary  = "keyhole"
)

// Platform is one platform package: the installer's platform id and the npm
// os/cpu/libc fields that make npm install only the matching one.
type Platform struct {
	ID, OS, CPU, Libc string
}

// Platforms are the packages a release has, in order.
var Platforms = []Platform{
	{ID: "linux-x64-gnu", OS: "linux", CPU: "x64", Libc: "glibc"},
	{ID: "linux-x64-musl", OS: "linux", CPU: "x64", Libc: "musl"},
}

// ErrUsage marks bad arguments or inputs; every other error means the inputs
// disagree, or a file can't be read or written.
var ErrUsage = errors.New("usage")

func usageErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrUsage, fmt.Sprintf(format, a...))
}

// semVer is SemVer 2.0.0, as the installer checks it (the version becomes a
// directory name there): no leading zeros, identifiers [0-9A-Za-z-]+.
var semVer = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)` +
	`(-(0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*))*)?` +
	`(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

// IsSemVer reports whether v is a SemVer 2.0.0 version.
func IsSemVer(v string) bool { return semVer.MatchString(v) }

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// marshal is JSON with sorted keys, two-space indent and a final newline.
func marshal(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		panic(err) // only maps, strings and slices are encoded here
	}
	return buf.Bytes()
}

func write(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, b, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode) // WriteFile's mode is cut by the umask
}

// readSums reads dist/SHA256SUMS as `sha256sum` writes it: one line,
// "<hex>  keyhole" (or "./keyhole").
func readSums(dist string) (string, error) {
	text, err := os.ReadFile(filepath.Join(dist, "SHA256SUMS"))
	if err != nil {
		return "", err
	}
	var want string
	for _, line := range strings.Split(string(text), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		hash, file, ok := strings.Cut(line, "  ")
		if !ok || len(hash) != 64 || strings.Trim(strings.ToLower(hash), "0123456789abcdef") != "" {
			return "", fmt.Errorf("SHA256SUMS: not `<sha256>  <file>`: %q", line)
		}
		if strings.TrimPrefix(file, "./") != Binary || want != "" {
			return "", fmt.Errorf("SHA256SUMS must list exactly one file, %s: %q", Binary, line)
		}
		want = strings.ToLower(hash)
	}
	if want == "" {
		return "", fmt.Errorf("SHA256SUMS lists nothing")
	}
	return want, nil
}

// Pack writes the meta package and the platform packages under out (which
// must not exist yet) from dist/keyhole and dist/SHA256SUMS. It returns the
// two byte-identical checksums.json copies, the --from root's first.
func Pack(version, dist, out string) ([]string, error) {
	if !IsSemVer(version) {
		return nil, usageErr("pack: not a semantic version (x.y.z): %q", version)
	}
	if _, err := os.Lstat(out); err == nil {
		return nil, usageErr("pack: %s already exists; pack into a new directory", out)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	// dist holds exactly the binary and its checksum: nothing is dropped from
	// the release, and nothing else is published by accident.
	entries, err := os.ReadDir(dist)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := []string{"SHA256SUMS", Binary}; !slices.Equal(names, want) {
		return nil, fmt.Errorf("pack: %s holds %q; a release is exactly %q", dist, names, want)
	}
	if fi, err := os.Lstat(filepath.Join(dist, Binary)); err != nil {
		return nil, err
	} else if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("pack: %s is not a regular file", filepath.Join(dist, Binary))
	}
	want, err := readSums(dist)
	if err != nil {
		return nil, fmt.Errorf("pack: %w", err)
	}
	bin, err := os.ReadFile(filepath.Join(dist, Binary))
	if err != nil {
		return nil, err
	}
	// The build job's own checksum must agree: a binary changed between the
	// build and the pack is refused rather than signed.
	hash := sha256Hex(bin)
	if hash != want {
		return nil, fmt.Errorf("pack: %s does not match SHA256SUMS", filepath.Join(dist, Binary))
	}

	files := map[string]string{}
	optional := map[string]string{}
	for _, p := range Platforms {
		name := Package + "-" + p.ID
		key := name + "/bin/" + Binary
		if err := write(filepath.Join(out, filepath.FromSlash(key)), bin, 0o755); err != nil {
			return nil, err
		}
		files[key] = hash
		manifest := map[string]any{
			"name":        name,
			"version":     version,
			"description": "The Keyhole server binary for " + p.ID,
			"license":     "MIT",
			"os":          []string{p.OS},
			"cpu":         []string{p.CPU},
			"libc":        []string{p.Libc},
			"files":       []string{"bin/"},
		}
		if err := write(filepath.Join(out, filepath.FromSlash(name), "package.json"), marshal(manifest), 0o644); err != nil {
			return nil, err
		}
		optional[name] = version
	}
	// No `scripts` and no `bin` anywhere: nothing runs at install time, and the
	// binary reaches a host only through the installer, after verification.
	meta := map[string]any{
		"name":                 Package,
		"version":              version,
		"description":          "The Keyhole server: verified by the vbcdx service installer, never run from node_modules",
		"license":              "MIT",
		"files":                []string{"checksums.json", "checksums.json.minisig"},
		"optionalDependencies": optional,
	}
	if err := write(filepath.Join(out, filepath.FromSlash(Package), "package.json"), marshal(meta), 0o644); err != nil {
		return nil, err
	}
	sums := marshal(map[string]any{"schema": 1, "package": Package, "version": version, "files": files})
	copies := []string{filepath.Join(out, "checksums.json"), filepath.Join(out, filepath.FromSlash(Package), "checksums.json")}
	for _, c := range copies {
		if err := write(c, sums, 0o644); err != nil {
			return nil, err
		}
	}
	return copies, nil
}

// checksums is checksums.json as the installer reads it.
type checksums struct {
	Schema  int               `json:"schema"`
	Package string            `json:"package"`
	Version string            `json:"version"`
	Files   map[string]string `json:"files"`
}

// TrustedComment is the comment a release signature must carry.
func TrustedComment(version string) string { return "vbcdx-release " + Package + " " + version }

// checkedRelease is what every signature covers, checked before one is made:
// the copies are byte-identical, the file is the installer's contract, and
// every file it lists is still the one it names (re-hashed from beside the
// first copy, the --from root). It returns the bytes to sign and the comment.
func checkedRelease(files []string) ([]byte, string, error) {
	if len(files) == 0 {
		return nil, "", usageErr("no checksums.json given")
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		return nil, "", err
	}
	for _, f := range files[1:] {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, "", err
		}
		if !bytes.Equal(b, data) {
			return nil, "", fmt.Errorf("%s differs from %s", f, files[0])
		}
	}
	var doc checksums
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, "", fmt.Errorf("%s is not JSON: %w", files[0], err)
	}
	if doc.Schema != 1 || doc.Package != Package || !IsSemVer(doc.Version) {
		return nil, "", fmt.Errorf("%s is not schema 1 for %s with a semantic version", files[0], Package)
	}
	if len(doc.Files) == 0 {
		return nil, "", fmt.Errorf("%s lists no files", files[0])
	}
	root := filepath.Dir(files[0])
	for key, want := range doc.Files {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(key)))
		if err != nil {
			return nil, "", err
		}
		if sha256Hex(b) != want {
			return nil, "", fmt.Errorf("%s does not match %s", key, files[0])
		}
	}
	return data, TrustedComment(doc.Version), nil
}

// Untrusted comments: what a signature's first line says it is.
const (
	ReleaseUntrustedComment  = "signature from the vbcdx release key"
	ContractUntrustedComment = "throwaway contract-test key: not a release signature"
)

// Sign signs files (identical copies of one checksums.json, the --from
// root's first) with the release key, writing <file>.minisig beside each.
// keyInput is the key as stdin carries it: an empty first line (the key has
// no password), then the minisign secret key file. A key that does not match
// pinnedPublicKey signs nothing. It returns the trusted comment.
func Sign(files []string, keyInput []byte, pinnedPublicKey string) (string, error) {
	pinned, err := ParsePublicKey(pinnedPublicKey)
	if err != nil {
		return "", fmt.Errorf("the pinned public key: %w", err)
	}
	keyText, err := splitKeyInput(keyInput)
	if err != nil {
		return "", err
	}
	sk, err := ParseSecretKey(keyText)
	if err != nil {
		return "", err
	}
	defer sk.wipe()
	if sk.Public() != pinned {
		return "", fmt.Errorf("the release secret key does not match the pinned public key")
	}
	data, comment, err := checkedRelease(files)
	if err != nil {
		return "", err
	}
	if err := writeSignatures(files, sk.Sign(data, comment, ReleaseUntrustedComment)); err != nil {
		return "", err
	}
	return comment, nil
}

// ContractSign signs files with a throwaway key made here and never stored,
// for the CI contract test. It returns the key's public line.
func ContractSign(files []string) (string, error) {
	data, comment, err := checkedRelease(files)
	if err != nil {
		return "", err
	}
	sk, err := GenerateKey()
	if err != nil {
		return "", err
	}
	defer sk.wipe()
	if err := writeSignatures(files, sk.Sign(data, comment, ContractUntrustedComment)); err != nil {
		return "", err
	}
	return sk.Public().String(), nil
}

func writeSignatures(files []string, sig []byte) error {
	for _, f := range files {
		if err := write(f+".minisig", sig, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// KeyInputMax is the most the key input may hold: a password line and a
// minisign secret key file (a few hundred bytes) with room to spare.
const KeyInputMax = 16 * 1024

// splitKeyInput returns the secret key file from the key input. Only a
// passwordless key is taken: an encrypted key would need a KDF and a
// password, and the CI dev key has neither.
func splitKeyInput(input []byte) ([]byte, error) {
	if len(input) > KeyInputMax {
		return nil, usageErr("the key input is over %d bytes; it is an empty line and a minisign key file", KeyInputMax)
	}
	first, rest, _ := bytes.Cut(input, []byte("\n"))
	if len(bytes.TrimSuffix(first, []byte("\r"))) != 0 {
		return nil, usageErr("the key input's first line must be empty: only a passwordless key is taken")
	}
	if len(bytes.TrimSpace(rest)) == 0 {
		return nil, usageErr("the key input must be an empty line, then the minisign secret key file")
	}
	return rest, nil
}

// Verify checks a packed root the way the installer does, for one platform:
// the signature over checksums.json under publicKey, the schema, package and
// version, the trusted comment, then the binary's SHA-256. The independent
// check is the installer's own JavaScript verifier (ci/installer-verifier);
// this one serves the Go tests and `keyhole-release verify`.
func Verify(root, publicKey, version, platform string) error {
	pk, err := ParsePublicKey(publicKey)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(root, "checksums.json"))
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(filepath.Join(root, "checksums.json.minisig"))
	if err != nil {
		return fmt.Errorf("the release is unsigned: %w", err)
	}
	comment, err := pk.Verify(raw, sig)
	if err != nil {
		return err
	}
	var doc checksums
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("checksums.json is signed but is not JSON")
	}
	if doc.Schema != 1 || doc.Package != Package || !IsSemVer(doc.Version) {
		return fmt.Errorf("checksums.json is not schema 1 for %s with a semantic version", Package)
	}
	if comment != TrustedComment(doc.Version) {
		return fmt.Errorf("the signature's trusted comment does not name this package and version")
	}
	if doc.Version != version {
		return fmt.Errorf("the release is version %s, not %s", doc.Version, version)
	}
	key := Package + "-" + platform + "/bin/" + Binary
	want, ok := doc.Files[key]
	if !ok {
		return fmt.Errorf("the release has no checksum for %s", key)
	}
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(key)))
	if err != nil {
		return err
	}
	if sha256Hex(b) != want {
		return fmt.Errorf("%s does not match its signed checksum", key)
	}
	return nil
}
