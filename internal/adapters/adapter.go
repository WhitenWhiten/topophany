// Package adapters defines the small contract shared by software adapters.
// Implementations report bounded runtime telemetry; the probe owns transport,
// scheduling, timestamps and the final observation envelope.
package adapters

import (
	"context"
	"errors"
	"os"
	"os/exec"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

type Command func(context.Context, string, ...string) ([]byte, error)

// Failure contains only a closed diagnostic code, never raw upstream text.
// Local distinguishes a monitor error from a failed target operation.
type Failure struct {
	Stage, Code string
	Local       bool
}

func (e *Failure) Error() string     { return e.Stage + ": " + e.Code }
func Local(stage, code string) error { return &Failure{Stage: stage, Code: code, Local: true} }
func Fail(stage string, err error) error {
	var policy *Failure
	if errors.As(err, &policy) && policy.Local && policy.Code == "management_network_denied" {
		return policy
	}

	var se *os.SyscallError
	if errors.As(err, &se) && (se.Syscall == "bind" || se.Syscall == "socket" || se.Syscall == "setsockopt") {
		return Local("setup", "local_"+se.Syscall+"_failed")
	}
	if errors.Is(err, os.ErrPermission) {
		return Local("setup", "local_permission_denied")
	}
	return &Failure{Stage: stage, Code: secure.ErrorCode(err)}
}

// Withheld marks peer-controlled text withheld after sending credentials.
const Withheld = "withheld_secret_request"

// NamesKeptLocal marks a secret-bearing read shown only to a local viewer:
// the user who supplied the secret, on this machine. Peer-controlled names
// are kept, sanitized as for a public peer; all other such text is withheld.
const NamesKeptLocal = "names_kept_local_secret_request"

// CommandFailure classifies only errors with reliable local provenance.
func CommandFailure(err error, fallback string) error {
	switch {
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, os.ErrNotExist):
		return Local("adapter", "command_not_found")
	case errors.Is(err, os.ErrPermission):
		return Local("adapter", "command_permission_denied")
	default:
		return Local("adapter", fallback)
	}
}
