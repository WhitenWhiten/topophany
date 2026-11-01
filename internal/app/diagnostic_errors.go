package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/direct"
)

type commandProblem struct {
	Code, Field, Message string
	Cause                error
}

func (e *commandProblem) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return e.Message
}
func (e *commandProblem) Unwrap() error { return e.Cause }

type errorDiagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
	Offset  int64  `json:"byte_offset,omitempty"`
	Next    string `json:"next_action"`
}

func classifyCommandError(err error) errorDiagnostic {
	out := errorDiagnostic{Code: "command_failed", Message: "The command could not complete.", Next: "Run the same command locally without --json-errors for detailed diagnostics."}
	var syntax *json.SyntaxError
	var field *json.UnmarshalTypeError
	var problem *commandProblem
	var source *direct.Error
	var transport *client.Failure
	switch {
	case errors.Is(err, config.ErrFileChanged):
		out.Code, out.Message, out.Next = "configuration_conflict", "The reviewed file changed.", "Reopen the file and review a new diff before applying."
	case errors.Is(err, os.ErrNotExist):
		out.Code, out.Message, out.Next = "file_not_found", "A required local file is missing.", "Check supplied file paths or run topo setup."
	case errors.Is(err, os.ErrPermission):
		out.Code, out.Message, out.Next = "permission_denied", "A required file or operation is not accessible.", "Check ownership and private-file permissions."
	case errors.As(err, &syntax):
		out.Code, out.Message, out.Offset = "invalid_json", "The input is not valid JSON.", syntax.Offset
		out.Next = "Correct the JSON near byte_offset and validate again."
	case errors.As(err, &field):
		out.Code, out.Message, out.Field, out.Offset = "invalid_field_type", "The field has an incompatible JSON type.", field.Field, field.Offset
		out.Next = "Correct the field type and validate again."
	case errors.As(err, &source):
		out.Code, out.Message, out.Next = source.Code, source.Message, "Inspect the named source and its saved authentication."
	case errors.Is(err, client.ErrProjectMismatch):
		out.Code, out.Message, out.Next = "project_mismatch", "The collector returned another project.", "Check the selected profile and collector project identity."
	case errors.Is(err, client.ErrNoCollector):
		out.Code, out.Message, out.Next = "collector_unavailable", "No collector answered.", "Run topo doctor with the same connection flags."
	case errors.As(err, &transport):
		out.Code, out.Message, out.Next = transport.Code, "The collector transport failed.", "Run topo doctor with the same connection flags."
	case errors.Is(err, context.DeadlineExceeded):
		out.Code, out.Message, out.Next = "deadline_exceeded", "The operation exceeded its deadline.", "Inspect the source's retry state before trying again."
	case errors.As(err, &problem):
		out.Code, out.Message, out.Field = problem.Code, problem.Message, problem.Field
		out.Next = "Review the input and run topo validate locally for details."
	}
	return out
}

// JSON error mode writes one bounded error envelope to stderr. Successful data
// and health-status exit codes retain their existing stdout contracts.
func (a *App) Run(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "--json-errors" {
		return a.run(ctx, args)
	}
	err := a.run(ctx, args[1:])
	if err == nil || errors.Is(err, flag.ErrHelp) || errors.Is(err, context.Canceled) {
		return err
	}
	code := 1
	var exit *ExitError
	if errors.As(err, &exit) {
		if exit.Err == nil {
			return err
		}
		code = exit.Code
		err = exit.Err
	}
	envelope := struct {
		Schema int             `json:"schema"`
		OK     bool            `json:"ok"`
		Error  errorDiagnostic `json:"error"`
	}{1, false, classifyCommandError(err)}
	if writeErr := printJSON(a.Err, envelope); writeErr != nil {
		return writeErr
	}
	return &ExitError{Code: code}
}
