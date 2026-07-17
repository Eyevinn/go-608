// Command go608-extract pulls CTA-608 out of a fragmented mp4 and writes it
// as WebVTT / SRT / SCC (format-only conversion is a mode). Not yet
// implemented — see https://github.com/Eyevinn/go-608/issues/25.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Eyevinn/go-608/internal"
)

const appName = "go608-extract"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s %s\n", appName, internal.GetVersion())
		return
	}

	fmt.Fprintf(os.Stderr, "%s: not yet implemented — see https://github.com/Eyevinn/go-608/issues/25\n", appName)
	os.Exit(2)
}
