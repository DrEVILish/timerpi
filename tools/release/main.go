// Command release makes and signs TimerPi builds for the boot-time updater
// (update package, VENUE-CLOUD §14).
//
//	go run ./tools/release keygen -key ~/timerpi-release.key
//	    writes the private key (keep it OFF the repo and the boxes) and the
//	    public key into update/release.pub, which the next build embeds.
//	go run ./tools/release sign -key ~/timerpi-release.key -bin bin/timerpi-arm64 -version 3.0.0 -arch arm64
//	    writes bin/timerpi-arm64.manifest.json next to the binary.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"timerpi/update"
)

func main() {
	if len(os.Args) < 2 {
		die("usage: release keygen|sign …")
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	key := fs.String("key", "", "private key file")
	bin := fs.String("bin", "", "binary to sign")
	ver := fs.String("version", "", "release version, e.g. 3.0.0")
	arch := fs.String("arch", "", "arm64 | amd64")
	pub := fs.String("pub", "update/release.pub", "public key file to write (keygen)")
	_ = fs.Parse(os.Args[2:])
	switch os.Args[1] {
	case "keygen":
		if *key == "" {
			die("keygen needs -key")
		}
		if _, err := os.Stat(*key); err == nil {
			die(*key + " exists; refusing to overwrite a release key")
		}
		pk, sk, err := ed25519.GenerateKey(rand.Reader)
		check(err)
		check(os.WriteFile(*key, []byte(base64.StdEncoding.EncodeToString(sk)+"\n"), 0o600))
		check(os.WriteFile(*pub, []byte(base64.StdEncoding.EncodeToString(pk)+"\n"), 0o644))
		fmt.Printf("private key: %s (keep it safe, never commit it)\npublic key:  %s\n", *key, *pub)
	case "sign":
		if *key == "" || *bin == "" || *ver == "" || *arch == "" {
			die("sign needs -key -bin -version -arch")
		}
		raw, err := os.ReadFile(*key)
		check(err)
		sk, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(sk) != ed25519.PrivateKeySize {
			die("bad private key")
		}
		f, err := os.Open(*bin)
		check(err)
		h := sha256.New()
		n, err := io.Copy(h, f)
		f.Close()
		check(err)
		m := update.Manifest{Version: *ver, Arch: *arch, SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}
		m.Sig = base64.StdEncoding.EncodeToString(ed25519.Sign(sk, m.SignedBytes()))
		b, _ := json.MarshalIndent(m, "", "  ")
		check(os.WriteFile(update.ManifestPath(*bin), b, 0o644))
		fmt.Println("wrote", update.ManifestPath(*bin))
	default:
		die("unknown command " + os.Args[1])
	}
}

func check(err error) {
	if err != nil {
		die(err.Error())
	}
}

func die(msg string) {
	fmt.Fprintln(os.Stderr, "release:", msg)
	os.Exit(1)
}
