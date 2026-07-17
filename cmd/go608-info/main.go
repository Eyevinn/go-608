// Command go608-info dumps the cc_data, token stream, and rendered Screen of
// a file or raw byte pairs (a debug tool). Not yet implemented — see
// https://github.com/Eyevinn/go-608/issues/24.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Eyevinn/go-608/internal"
)

const appName = "go608-info"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s %s\n", appName, internal.GetVersion())
		return
	}

	fmt.Fprintf(os.Stderr, "%s: not yet implemented — see https://github.com/Eyevinn/go-608/issues/24\n", appName)
	os.Exit(2)
}
