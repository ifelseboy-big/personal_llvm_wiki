package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

const ProtocolVersion = "1.0"

const (
	ExitOK          = 0
	ExitUsage       = 2
	ExitConfig      = 3
	ExitNotFound    = 4
	ExitConflict    = 5
	ExitValidation  = 6
	ExitSafety      = 7
	ExitLock        = 8
	ExitIO          = 9
	ExitIndex       = 10
	ExitMigration   = 11
	ExitUnsupported = 12
	ExitInternal    = 70
)

type AppError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	Retryable bool           `json:"retryable"`
	ExitCode  int            `json:"-"`
	Cause     error          `json:"-"`
}

func (e *AppError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *AppError) Unwrap() error { return e.Cause }

func E(code, message string, exit int, cause error) *AppError {
	err := &AppError{Code: code, Message: message, ExitCode: exit, Cause: cause}
	if cause != nil {
		err.Details = map[string]any{"reason": cause.Error()}
	}
	return err
}

type WikiRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type Response struct {
	SchemaVersion string         `json:"schema_version"`
	OK            bool           `json:"ok"`
	Command       string         `json:"command"`
	ToolVersion   string         `json:"tool_version"`
	Wiki          *WikiRef       `json:"wiki,omitempty"`
	Data          any            `json:"data,omitempty"`
	Warnings      []string       `json:"warnings"`
	AffectedFiles []string       `json:"affected_files"`
	Error         *AppError      `json:"error,omitempty"`
	Meta          map[string]any `json:"meta,omitempty"`
}

type Runtime struct {
	JSON          bool
	NoInteractive bool
	DryRun        bool
	Quiet         bool
	Verbose       bool
	WikiArg       string
	Color         string
	Command       string
	Wiki          *WikiRef
	Stdin         io.Reader
	Stdout        io.Writer
	Stderr        io.Writer
}

func NewRuntime() *Runtime {
	return &Runtime{Color: "auto", Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
}

func (r *Runtime) Success(command string, wiki *WikiRef, data any, warnings, files []string) error {
	if warnings == nil {
		warnings = []string{}
	}
	if files == nil {
		files = []string{}
	}
	if r.JSON {
		return json.NewEncoder(r.Stdout).Encode(Response{
			SchemaVersion: ProtocolVersion,
			OK:            true,
			Command:       command,
			ToolVersion:   Version,
			Wiki:          wiki,
			Data:          data,
			Warnings:      warnings,
			AffectedFiles: files,
		})
	}
	if r.Quiet {
		return nil
	}
	if data == nil {
		_, err := fmt.Fprintln(r.Stdout, "ok")
		return err
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(r.Stdout, string(b))
	return err
}

func RenderFailure(cmd *cobra.Command, err error) int {
	rt, _ := cmd.Context().Value(runtimeKey{}).(*Runtime)
	if rt == nil {
		rt = NewRuntime()
	}
	var ae *AppError
	if !errors.As(err, &ae) {
		if isUsageError(err.Error()) {
			ae = E("INVALID_ARGUMENT", "invalid command arguments", ExitUsage, err)
		} else {
			ae = E("INTERNAL_ERROR", "unexpected internal error", ExitInternal, err)
		}
	}
	if rt.JSON {
		command := rt.Command
		if command == "" {
			command = commandPath(cmd)
		}
		_ = json.NewEncoder(rt.Stdout).Encode(Response{
			SchemaVersion: ProtocolVersion,
			OK:            false,
			Command:       command,
			ToolVersion:   Version,
			Wiki:          rt.Wiki,
			Warnings:      []string{},
			AffectedFiles: []string{},
			Error:         ae,
		})
	} else {
		fmt.Fprintf(rt.Stderr, "error[%s]: %s\n", ae.Code, ae.Error())
	}
	if ae.ExitCode == 0 {
		return ExitInternal
	}
	return ae.ExitCode
}

func isUsageError(message string) bool {
	for _, marker := range []string{
		"unknown command", "unknown flag", "required flag", "arg(s)",
		"requires at least", "requires at most", "cannot be used together",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func commandPath(cmd *cobra.Command) string {
	if cmd == nil {
		return "unknown"
	}
	return cmd.CommandPath()
}

type runtimeKey struct{}
