// Command testmsg finds and classifies error checks in Go test files.
//
// Usage:
//
//	testmsg [-path importpath] [-checks] dir
//
// It prints a JSON summary of the source tree at dir. With -checks it also
// includes every check site and unresolved call.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/icholy/testmsg-survey/internal/checks"
)

func main() {
	log.SetFlags(0)
	path := flag.String("path", "", "import path to use for code outside any go.mod")
	full := flag.Bool("checks", false, "include every check site and unresolved call")
	flag.Parse()
	if flag.NArg() != 1 {
		log.Fatal("usage: testmsg [-path importpath] [-checks] dir")
	}
	m, err := checks.Load(flag.Arg(0), *path)
	if err != nil {
		log.Fatal(err)
	}
	res := m.Analyze()
	out := checks.Summarize(m, res)
	if *full {
		out.Checks = res.Checks
		out.Unresolved = res.Unresolved
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		log.Fatal(err)
	}
}
