// Command go608-clock generates wall-clock CTA-608 captions and splices them
// as SEI into a fragmented mp4 (the first-milestone demo). Not yet
// implemented — see https://github.com/Eyevinn/go-608/issues/23.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Eyevinn/go-608/internal"
)

const appName = "go608-clock"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s %s\n", appName, internal.GetVersion())
		return
	}

	fmt.Fprintf(os.Stderr, "%s: not yet implemented — see https://github.com/Eyevinn/go-608/issues/23\n", appName)
	os.Exit(2)
}
