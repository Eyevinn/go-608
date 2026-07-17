// Command go608-inject injects CTA-608 as SEI into a fragmented mp4 from
// WebVTT / SRT / SCC input (format-only conversion is a mode). Not yet
// implemented — see https://github.com/Eyevinn/go-608/issues/26.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Eyevinn/go-608/internal"
)

const appName = "go608-inject"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s %s\n", appName, internal.GetVersion())
		return
	}

	fmt.Fprintf(os.Stderr, "%s: not yet implemented — see https://github.com/Eyevinn/go-608/issues/26\n", appName)
	os.Exit(2)
}
