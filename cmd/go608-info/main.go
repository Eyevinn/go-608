// Command go608-info dumps the cc_data, token stream, and rendered Screen of
// a file or raw byte pairs (a debug tool). Not yet implemented — see
// https://github.com/Eyevinn/go-608/issues/24.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Eyevinn/go-608/internal"
)

const (
	appName = "go608-info"
)

var usg = `%s dumps the cc_data, token stream, and rendered Screen of a file
or raw byte pairs (a debug tool).

Usage of %s:
`

type options struct {
	version bool
}

func parseOptions(fs *flag.FlagSet, args []string) (*options, error) {
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, usg, appName, appName)
		fmt.Fprintf(os.Stderr, "\n%s [options]\n\noptions:\n", appName)
		fs.PrintDefaults()
	}

	opts := options{}
	fs.BoolVar(&opts.version, "version", false, "Get go-608 version")

	err := fs.Parse(args[1:])
	return &opts, err
}

func main() {
	if err := run(os.Args, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, w io.Writer) error {
	fs := flag.NewFlagSet(appName, flag.ContinueOnError)
	opts, err := parseOptions(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if opts.version {
		fmt.Fprintf(w, "%s %s\n", appName, internal.GetVersion())
		return nil
	}

	return fmt.Errorf("not yet implemented — see https://github.com/Eyevinn/go-608/issues/24")
}
