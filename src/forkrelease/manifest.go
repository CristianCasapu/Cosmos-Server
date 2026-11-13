package forkrelease

// Fork addition: the signature file of a release and its verification.
//
// A release carries one small text file, cosmos-cloud-<version>.sig:
//
//	cosmos-fork-release-v1
//	version 0.24.0-fork.10
//	sha256 <64 hex digits> cosmos-cloud-0.24.0-fork.10-amd64.zip
//	sha256 <64 hex digits> cosmos-cloud-0.24.0-fork.10-arm64.zip
//	signature <base64 of an Ed25519 signature over every byte above this line>
//
// What is signed is the SHA-256 of each zip together with the version and the
// file names, so a signed zip of an older release cannot be passed off as a
// newer one, nor one architecture's zip as the other's. There may be a
// second signature line (while a key is being replaced, releases are signed
// with both); the file is accepted when one signature verifies under one of
// the trusted keys.
//
// The file's name carries no architecture on purpose: the updater of the
// releases before signing takes the last asset whose name contains "amd64"
// (or "arm64") and does not end in ".md5" as the zip to install.

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	manifestHeader = "cosmos-fork-release-v1"

	// MaxSignatureFile bounds the signature file.
	MaxSignatureFile = 4096

	maxManifestFiles      = 8
	maxManifestSignatures = 4
)

var (
	ErrNoReleaseKey  = errors.New("no release key is compiled into this build, so no release can be verified")
	ErrBadSignature  = errors.New("the signature file is not signed with a release key of this build")
	ErrMalformed     = errors.New("the signature file is not in the expected form")
	ErrWrongVersion  = errors.New("the signature file is for another version")
	ErrNoSuchFile    = errors.New("the signature file does not list this file")
	ErrWrongChecksum = errors.New("the file does not have the SHA-256 the signature file lists")
)

var (
	versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})-fork\.([1-9][0-9]{0,8})$`)
	sha256Pattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Manifest is the signed content of a signature file.
type Manifest struct {
	Version string            // for example 0.24.0-fork.10
	Files   map[string]string // file name -> SHA-256, lower-case hex
}

// Build returns the build number of a fork version ("0.24.0-fork.10", with
// or without a leading "v"): the number that orders fork releases.
func Build(version string) (int, bool) {
	m := versionPattern.FindStringSubmatch(strings.TrimPrefix(version, "v"))
	if m == nil {
		return 0, false
	}
	build, err := strconv.Atoi(m[4])
	if err != nil {
		return 0, false
	}
	return build, true
}

// ZipName is the name of a release's zip for an architecture.
func ZipName(version string, arch string) string {
	return "cosmos-cloud-" + version + "-" + arch + ".zip"
}

// SignatureName is the name of a release's signature file.
func SignatureName(version string) string {
	return "cosmos-cloud-" + version + ".sig"
}

func knownArch(arch string) bool {
	return arch == "amd64" || arch == "arm64"
}

// parseSigned splits a signature file into the signed bytes, their content
// and the signatures. It checks the form only.
func parseSigned(data []byte) (signed []byte, manifest Manifest, signatures [][]byte, err error) {
	bad := func(what string) ([]byte, Manifest, [][]byte, error) {
		return nil, Manifest{}, nil, fmt.Errorf("%w: %s", ErrMalformed, what)
	}
	if len(data) == 0 || len(data) > MaxSignatureFile {
		return bad("size")
	}
	for _, c := range data {
		if c != '\n' && (c < 0x20 || c > 0x7e) {
			return bad("characters")
		}
	}
	if data[len(data)-1] != '\n' {
		return bad("last line")
	}
	lines := strings.Split(string(data[:len(data)-1]), "\n")
	if len(lines) < 4 || len(lines) > 2+maxManifestFiles+maxManifestSignatures {
		return bad("number of lines")
	}
	if lines[0] != manifestHeader {
		return bad("first line")
	}

	version, ok := strings.CutPrefix(lines[1], "version ")
	if !ok || !versionPattern.MatchString(version) {
		return bad("version")
	}
	manifest = Manifest{Version: version, Files: map[string]string{}}

	i, signedLength := 2, len(lines[0])+1+len(lines[1])+1
	for ; i < len(lines) && strings.HasPrefix(lines[i], "sha256 "); i++ {
		fields := strings.Split(lines[i], " ")
		if len(fields) != 3 || !sha256Pattern.MatchString(fields[1]) {
			return bad("checksum line")
		}
		name := fields[2]
		if name != ZipName(version, "amd64") && name != ZipName(version, "arm64") {
			return bad("file name")
		}
		if _, twice := manifest.Files[name]; twice {
			return bad("file listed twice")
		}
		manifest.Files[name] = fields[1]
		signedLength += len(lines[i]) + 1
	}
	if len(manifest.Files) == 0 || len(manifest.Files) > maxManifestFiles {
		return bad("no file")
	}

	for ; i < len(lines); i++ {
		encoded, ok := strings.CutPrefix(lines[i], "signature ")
		if !ok {
			return bad("signature line")
		}
		signature, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || len(signature) != ed25519.SignatureSize {
			return bad("signature")
		}
		signatures = append(signatures, signature)
	}
	if len(signatures) == 0 || len(signatures) > maxManifestSignatures {
		return bad("number of signatures")
	}
	return data[:signedLength], manifest, signatures, nil
}

// verifyWith checks a signature file against the given keys and returns what
// it signs.
func verifyWith(keys []ed25519.PublicKey, data []byte) (Manifest, error) {
	if len(keys) == 0 {
		return Manifest{}, ErrNoReleaseKey
	}
	signed, manifest, signatures, err := parseSigned(data)
	if err != nil {
		return Manifest{}, err
	}
	for _, signature := range signatures {
		for _, key := range keys {
			if len(key) == ed25519.PublicKeySize && ed25519.Verify(key, signed, signature) {
				return manifest, nil
			}
		}
	}
	return Manifest{}, ErrBadSignature
}

// Verify checks a signature file against the keys compiled into this binary.
func Verify(data []byte) (Manifest, error) {
	return verifyWith(compiledKeys(), data)
}

// checksumFor returns the SHA-256 the manifest lists for the zip of a
// version and architecture.
func (m Manifest) checksumFor(version string, arch string) (string, error) {
	if !knownArch(arch) {
		return "", fmt.Errorf("%w: architecture %s", ErrNoSuchFile, arch)
	}
	if m.Version != strings.TrimPrefix(version, "v") {
		return "", ErrWrongVersion
	}
	sum, ok := m.Files[ZipName(m.Version, arch)]
	if !ok {
		return "", ErrNoSuchFile
	}
	return sum, nil
}
