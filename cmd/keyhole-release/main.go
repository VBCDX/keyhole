// Command keyhole-release packs and signs the Keyhole server's npm release
// (internal/release). It is a CI and release-operator tool, not part of the
// server.
//
//	keyhole-release pack VERSION DIST OUT
//	keyhole-release sign --public-key FILE CHECKSUMS...    (key on stdin)
//	keyhole-release contract-sign --public-key-out FILE CHECKSUMS...
//	keyhole-release verify ROOT PUBLIC-KEY-FILE VERSION
//	keyhole-release keygen --secret-out FILE --public-out FILE
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/VBCDX/keyhole/internal/release"
)

const usage = `usage: keyhole-release <command>

  pack VERSION DIST OUT      pack DIST/keyhole (checked against DIST/SHA256SUMS)
                             into the @vbcdx/keyhole npm packages under OUT (new)
  sign --public-key FILE CHECKSUMS...
                             sign identical checksums.json copies (the --from
                             root's first) with the release key read from stdin:
                             an empty line, then the passwordless minisign secret
                             key file. A key that is not FILE's signs nothing
  contract-sign --public-key-out FILE CHECKSUMS...
                             sign with a throwaway key (the CI contract test)
  verify ROOT PUBLIC-KEY-FILE VERSION
                             check a signed root for every platform
  keygen --secret-out FILE --public-out FILE
                             make a passwordless key (tests only; FILE 0600)
`

func main() {
	err := run(os.Args[1:], os.Stdin, os.Stdout)
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "keyhole-release:", err)
	if errors.Is(err, release.ErrUsage) {
		os.Exit(2)
	}
	os.Exit(1)
}

func usageErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", release.ErrUsage, fmt.Sprintf(format, a...))
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return usageErr("no command\n%s", usage)
	}
	cmd, args := args[0], args[1:]
	switch cmd {
	case "pack":
		if len(args) != 3 {
			return usageErr("pack VERSION DIST OUT")
		}
		copies, err := release.Pack(args[0], args[1], args[2])
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "packed %s %s: %s\n", release.Package, args[0], copies[0])
		return nil
	case "sign":
		fs := flag.NewFlagSet("sign", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		pub := fs.String("public-key", "", "the pinned public key file")
		if err := fs.Parse(args); err != nil || *pub == "" || fs.NArg() == 0 {
			return usageErr("sign --public-key FILE CHECKSUMS...")
		}
		pinned, err := os.ReadFile(*pub)
		if err != nil {
			return err
		}
		// Read at most one byte past the limit, so an oversized input is refused.
		input, err := io.ReadAll(io.LimitReader(stdin, release.KeyInputMax+1))
		defer func() {
			for i := range input {
				input[i] = 0
			}
		}()
		if err != nil {
			return fmt.Errorf("reading the key from stdin: %w", err)
		}
		comment, err := release.Sign(fs.Args(), input, string(pinned))
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "signed: %s\n", comment)
		return nil
	case "contract-sign":
		fs := flag.NewFlagSet("contract-sign", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		out := fs.String("public-key-out", "", "where the throwaway public key line is written")
		if err := fs.Parse(args); err != nil || *out == "" || fs.NArg() == 0 {
			return usageErr("contract-sign --public-key-out FILE CHECKSUMS...")
		}
		pub, err := release.ContractSign(fs.Args())
		if err != nil {
			return err
		}
		return os.WriteFile(*out, []byte(pub+"\n"), 0o644)
	case "verify":
		if len(args) != 3 {
			return usageErr("verify ROOT PUBLIC-KEY-FILE VERSION")
		}
		pub, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		for _, p := range release.Platforms {
			if err := release.Verify(args[0], string(pub), args[2], p.ID); err != nil {
				return fmt.Errorf("%s: %w", p.ID, err)
			}
			fmt.Fprintf(stdout, "verified %s %s for %s\n", release.Package, args[2], p.ID)
		}
		return nil
	case "keygen":
		fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		secretOut := fs.String("secret-out", "", "the secret key file (0600, must not exist)")
		publicOut := fs.String("public-out", "", "the public key file")
		if err := fs.Parse(args); err != nil || *secretOut == "" || *publicOut == "" || fs.NArg() != 0 {
			return usageErr("keygen --secret-out FILE --public-out FILE")
		}
		sk, err := release.GenerateKey()
		if err != nil {
			return err
		}
		f, err := os.OpenFile(*secretOut, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		if _, err := f.Write(sk.File()); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		return os.WriteFile(*publicOut, []byte(sk.Public().File()), 0o644)
	case "help", "-h", "--help":
		_, err := io.WriteString(stdout, usage)
		return err
	default:
		return usageErr("unknown command %q\n%s", cmd, usage)
	}
}
