package cmdutil

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// SecretFlag is a secret given as a value (which may be @file or @-) or as the
// name of an environment variable. Prefer the variable or a file so that the
// secret stays out of shell history.
type SecretFlag struct {
	Value string
	Env   string
}

// Given is true when either form was passed.
func (s SecretFlag) Given() bool { return s.Value != "" || s.Env != "" }

// Read returns the secret. When neither form was passed it asks for it on a
// terminal, and fails without one. flag is the value flag's name, such as
// "password"; label is what the prompt calls it.
func (s SecretFlag) Read(app *App, flag, label string) (string, error) {
	switch {
	case s.Value != "":
		v, err := app.ReadValue(s.Value)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(v, "\r\n"), nil
	case s.Env != "":
		v := os.Getenv(s.Env)
		if v == "" {
			return "", UsageError("environment variable %s is empty or not set", s.Env)
		}
		return v, nil
	}
	if !app.Interactive() {
		return "", UsageError("--%s or --%s-env is required", flag, flag)
	}
	f, ok := app.In.(*os.File)
	if !ok {
		return "", UsageError("--%s or --%s-env is required", flag, flag)
	}
	fmt.Fprintf(app.Err, "%s: ", label)
	b, err := term.ReadPassword(int(f.Fd()))
	fmt.Fprintln(app.Err)
	if err != nil {
		return "", err
	}
	if len(b) == 0 {
		return "", UsageError("%s is required", label)
	}
	return string(b), nil
}

// AddSecretFlags adds --<name> and --<name>-env.
func AddSecretFlags(cmd *cobra.Command, s *SecretFlag, name, what string) {
	cmd.Flags().StringVar(&s.Value, name, "", what+"; @file reads a file and @- reads standard input")
	cmd.Flags().StringVar(&s.Env, name+"-env", "", "name of an environment variable that holds the "+what)
}

// ReadArguments reads tool arguments given as JSON, @file or @-. An empty
// value is an empty object.
func ReadArguments(app *App, input string) (*json.RawMessage, error) {
	if input == "" {
		raw := json.RawMessage("{}")
		return &raw, nil
	}
	v, err := app.ReadValue(input)
	if err != nil {
		return nil, err
	}
	raw := json.RawMessage(strings.TrimSpace(v))
	if !json.Valid(raw) {
		return nil, UsageError("--input is not valid JSON")
	}
	return &raw, nil
}

// PollUntil calls check every interval until it returns a result or an error.
func PollUntil[T any](ctx context.Context, interval time.Duration, check func(ctx context.Context) (*T, error)) (*T, error) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		r, err := check(ctx)
		if err != nil || r != nil {
			return r, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}
