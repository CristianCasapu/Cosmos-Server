package forkrelease

// Fork addition: when a restore that is owed cannot finish, the launcher
// says, at every attempt, what is wrong and how to get out of it, in the
// journal and in cosmos-update.README in the install folder. Every command
// printed is meant to be typed as it stands on a Debian server.

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// serviceName is the systemd unit Cosmos runs as (src/index.go).
const serviceName = "CosmosCloud"

// keptDamagedError: a file of the copy kept is missing or not what it was.
type keptDamagedError struct{ path string }

func (e *keptDamagedError) Error() string {
	return "the kept copy of " + SafeText(e.path) + " is missing or does not have its SHA-256"
}

// obstacleError: something in the install folder is in the way.
type obstacleError struct{ path, what string }

func (e *obstacleError) Error() string { return SafeText(e.path) + " " + e.what }

// busyError: an update is refused because an earlier one is being undone.
type busyError struct{ version, earlier string }

func (e *busyError) Error() string {
	return "an earlier update is still being undone (" + SafeText(e.earlier) + "), so " + SafeText(e.version) + " is not installed now; it is tried again at the next update"
}

func readmePath(folder string) string { return filepath.Join(folder, readmeName) }

// resolved: nothing needs a person (any more).
func resolved(folder string) {
	if _, err := os.Lstat(readmePath(folder)); err == nil {
		os.Remove(readmePath(folder))
	}
}

func purposeWords(trial Trial) string {
	switch trial.Purpose {
	case purposeRollback:
		return "Version " + trial.NewVersion + " did not start properly, and Cosmos is going back to version " + trial.PreviousVersion + "."
	case purposeUnrecorded:
		return "The trial of version " + trial.NewVersion + " could not be recorded, and Cosmos is going back to version " + trial.PreviousVersion + "."
	case purposeUndo:
		return "Version " + trial.NewVersion + " could not be unpacked (" + trial.Cause + "), and Cosmos is putting back version " + trial.PreviousVersion + "."
	case purposeInterrupted:
		return "The installation of version " + trial.NewVersion + " was cut short, and Cosmos is putting back version " + trial.PreviousVersion + "."
	}
	return "The state of an update could not be read."
}

// quote makes a word for sh.
func quote(word string) string {
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
}

// endCommand removes the state, the marks and the update file and its
// signature, and after a rollback notes the version as failed.
func endCommand(folder string, trial Trial) string {
	command := "rm -f " + quote(trialPath(folder)) + " " + quote(folder) + "/" + markPrefix + "* " + quote(filepath.Join(folder, StagedZip)) + " " + quote(filepath.Join(folder, StagedSignature))
	if (trial.Purpose == purposeRollback || trial.Purpose == purposeUnrecorded) && trial.NewVersion != "" && trial.NewVersion != trial.PreviousVersion {
		command += " && echo " + quote(trial.NewVersion) + " >> " + quote(filepath.Join(folder, failedName))
	}
	return command
}

// reinstallKeys are the keys the hand reinstall checks the signature file
// with: those compiled into this launcher (tests use their own).
var reinstallKeys = compiledKeys

// ed25519DERPrefix is what goes before the 32 bytes of an Ed25519 public
// key in DER (SubjectPublicKeyInfo), as octal escapes for printf.
const ed25519DERPrefix = `\060\052\060\005\006\003\053\145\160\003\041\000`

// reinstallCommands put the release back by hand as the launcher would. They
// are run as one block by `sh -e`, so the first step that fails stops all:
// the zip and its signature file from the fork's releases; the signature
// file verified with the release keys of this installation (as the launcher
// does) and its version line checked; the zip checked against its signed
// line; unpacked over the folder without its start.sh, the root folder of
// the zip left out. Nothing is copied before all of that passed.
func reinstallCommands(folder string, trial Trial, version string) []string {
	zip, sig := ZipName(version, runtime.GOARCH), SignatureName(version)
	base := "https://github.com/" + releaseRepo + "/releases/download/v" + version + "/"
	work := "/tmp/cosmos-reinstall"
	commands := []string{
		"command -v curl >/dev/null && command -v unzip >/dev/null && command -v openssl >/dev/null || apt-get install -y curl unzip openssl",
		"rm -rf " + work + " && mkdir " + work + " && cd " + work,
		"curl -fLO " + base + zip + " && curl -fLO " + base + sig,
		"grep -v '^signature ' " + sig + " > signed",
		"grep -qx " + quote("version "+version) + " signed",
	}
	for i, key := range reinstallKeys() {
		pem := "key-" + strconv.Itoa(i+1) + ".pem"
		commands = append(commands, "printf '"+ed25519DERPrefix+"' > key.der && printf '%s' "+quote(base64.StdEncoding.EncodeToString(key))+
			" | base64 -d >> key.der && openssl pkey -pubin -inform DER -in key.der -out "+pem)
	}
	commands = append(commands,
		"grep '^signature ' "+sig+" | while read -r _ s; do printf '%s' \"$s\" | base64 -d > s.bin; for k in key-*.pem; do openssl pkeyutl -verify -pubin -inkey \"$k\" -rawin -in signed -sigfile s.bin >/dev/null 2>&1 && touch verified || true; done; done",
		"test -f verified",
		"grep ' "+zip+"$' signed | sed 's/^sha256 \\([0-9a-f]*\\) /\\1  /' | sha256sum -c",
		"unzip -q "+zip+" -d unpacked && rm -f unpacked/*/start.sh",
		"cp -a unpacked/*/. "+quote(folder)+"/",
		"rm -f "+quote(filepath.Join(folder, pendingLauncher))+" "+quote(filepath.Join(folder, "cosmos-launcher-arm64.updated")),
		endCommand(folder, trial),
	)
	return commands
}

// reinstallEnd ends the block the README prints the commands in, one block
// to paste into a root shell (the end line stays at the start of its line).
const reinstallEnd = "COSMOS_REINSTALL"

// explain prints what is wrong and the ways out, and writes them to the
// README (overwritten at every attempt).
func explain(folder string, trial Trial, problem error, env launcherEnv) {
	if trial.PreviousVersion == "" {
		if manifest, err := readKept(folder); err == nil {
			trial.PreviousVersion, trial.NewVersion = manifest.PreviousVersion, manifest.NewVersion
		}
	}
	stop, start := "systemctl stop "+serviceName, "systemctl start "+serviceName
	lines := []string{
		"Cosmos update: a restore of the program files is owed and cannot finish (" + time.Now().UTC().Format("2006-01-02 15:04 UTC") + ").",
		"",
		"What happened: " + purposeWords(trial),
		"What is wrong: " + SafeText(problem.Error()) + ".",
		"Cosmos is NOT started until this is resolved, so that it never runs a mix of two versions. The launcher tries again at every start (about every " + strconv.Itoa(int((RestorePause+unitRestartSec)/time.Second)) + " seconds); once the cause is gone it finishes by itself.",
		"",
		"First stop the service: " + stop + " . While it runs, systemd starts the launcher again every few seconds, and the launcher works in the same folder.",
		"Then the first way out that applies, and last: " + start,
	}
	step := 1
	add := func(text string) {
		lines = append(lines, strconv.Itoa(step)+". "+text)
		step++
	}
	var damaged *keptDamagedError
	var obstacle *obstacleError
	switch {
	case errors.As(problem, &obstacle):
		add("Remove or rename " + filepath.Join(folder, obstacle.path) + " (it " + obstacle.what + "). The next start finishes the restore by itself.")
	case errors.As(problem, &damaged), trial.PreviousVersion == "":
	default:
		add("Fix the cause above. A full disk: df -h " + folder + ". A read-only file system: findmnt -T " + folder + " (or, without findmnt: df " + folder + ", and look for \"ro\" in mount's line for that file system). The next start finishes the restore by itself.")
	}
	if trial.PreviousVersion != "" && releaseRepo != "" {
		add("Install " + trial.PreviousVersion + " again by hand, as the launcher does (needs the internet). Paste the whole block below, from its first line to " + reinstallEnd + ", into a root shell: it stops at the first step that fails, and copies nothing before the release's signature file verified with the release keys of this installation and the zip matched it:")
		lines = append(lines, "sh -e <<'"+reinstallEnd+"'")
		lines = append(lines, reinstallCommands(folder, trial, trial.PreviousVersion)...)
		lines = append(lines, reinstallEnd)
	}
	way := "start the files as they are now: " + endCommand(folder, trial) + " . They may be a mix of " + nonEmpty(trial.NewVersion, "the new version") + " and " + nonEmpty(trial.PreviousVersion, "the version before") + " that does not start or does not work; nothing is put back any more."
	if step == 1 {
		add("Only this applies: " + way)
	} else {
		add("Or " + way)
	}
	text := strings.Join(lines, "\n") + "\n"
	for _, line := range lines {
		if line != "" {
			fmt.Fprintln(env.out, "[ERROR] Update: "+line)
		}
	}
	if err := writeAtomic(readmePath(folder), []byte(text)); err != nil {
		fmt.Fprintln(env.out, "[ERROR] Update: "+readmePath(folder)+" could not be written: "+SafeText(pathless(err).Error()))
	}
}

// attention writes a README for a server that runs but whose update needs a
// person (the bookkeeping of a finished restore could not be written).
func attention(folder string, what string, env launcherEnv) {
	text := "Cosmos update: needs attention (" + time.Now().UTC().Format("2006-01-02 15:04 UTC") + ").\n\n" + what + "\n"
	fmt.Fprintln(env.out, "[ERROR] Update: "+what)
	writeAtomic(readmePath(folder), []byte(text))
}

func nonEmpty(value string, otherwise string) string {
	if value == "" {
		return otherwise
	}
	return value
}
