package storage

// Fork addition: what the storage forms are told when the server refuses or
// a step fails, and the pieces of the machine the mount code touches, behind
// variables so that the tests touch nothing.
//
// A refusal carries its own HTTP status and code (STO0xx): the forms show the
// sentence under the field and can tell the cases apart by the code. Any
// other error keeps the answer upstream gave.

import (
	"errors"
	"net/http"
	"os"

	"github.com/azukaar/cosmos-server/src/utils"
)

// forkStorageError is a refusal or a failure the panel shows as it is.
type forkStorageError struct {
	Status  int
	Code    string
	Message string
}

func (e *forkStorageError) Error() string { return e.Message }

func refuse(status int, code string, message string) error {
	return &forkStorageError{Status: status, Code: code, Message: message}
}

// storageErrorCode returns the code of a refusal, "" for any other error.
func storageErrorCode(err error) string {
	var refusal *forkStorageError
	if errors.As(err, &refusal) {
		return refusal.Code
	}
	return ""
}

// forkHTTPError answers a refusal with its own status, code and sentence;
// any other error as upstream did (prefix + error, the given status and code).
func forkHTTPError(w http.ResponseWriter, err error, prefix string, status int, code string) {
	var refusal *forkStorageError
	if errors.As(err, &refusal) {
		utils.HTTPError(w, cleanText(refusal.Message, 1000), refusal.Status, refusal.Code)
		return
	}
	utils.HTTPError(w, prefix+err.Error(), status, code)
}

// forkRun runs one storage command with fixed arguments through
// runStorageCommand; a failure says which step, the exit status and what the
// command said.
func forkRun(step string, name string, args ...string) (string, error) {
	out, err := runStorageCommand(name, args...)
	if err != nil {
		return out, commandFailure(step, name, out, err)
	}
	return out, nil
}

// The files and folders the mount code writes, behind variables for the tests.
var (
	fstabFile         = "/etc/fstab"
	makeMountFolder   = os.Mkdir
	removeMountFolder = os.Remove
)
