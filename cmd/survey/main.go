// Command survey runs the analyser over a corpus tier.
//
// Usage:
//
//	go run ./cmd/survey -tier go      corpus/go.tsv
//	go run ./cmd/survey -tier popular corpus/popular.tsv
//
// For each repository in the TSV file (columns: a "repo" column with
// owner/name and a "commit" column), it downloads the GitHub tarball at the
// pinned commit, keeps only .go and go.mod files, analyses it and writes
//
//	results/<tier>/<owner>__<name>/summary.json
//	results/<tier>/<owner>__<name>/checks.jsonl.gz
//	results/<tier>/<owner>__<name>/unresolved.jsonl.gz
//
// Repositories whose summary.json already exists are skipped, so an
// interrupted run can be resumed.
package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/icholy/testmsg-survey/internal/checks"
)

// Entry is one repository from a corpus file.
type Entry struct {
	Repo   string
	Commit string
}

// RepoSummary is written to summary.json.
type RepoSummary struct {
	Tier   string `json:"tier"`
	Repo   string `json:"repo"`
	Commit string `json:"commit"`
	Error  string `json:"error,omitempty"`
	checks.Summary
}

func main() {
	log.SetFlags(0)
	tier := flag.String("tier", "", "tier name, used for the output directory")
	out := flag.String("out", "results", "output directory")
	jobs := flag.Int("j", 4, "number of repositories to process in parallel")
	work := flag.String("work", os.TempDir(), "directory for temporary checkouts")
	flag.Parse()
	if *tier == "" || flag.NArg() != 1 {
		log.Fatal("usage: survey -tier name corpus.tsv")
	}
	entries, err := readCorpus(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	ch := make(chan Entry)
	var wg sync.WaitGroup
	for range *jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range ch {
				dir := filepath.Join(*out, *tier, strings.ReplaceAll(e.Repo, "/", "__"))
				if _, err := os.Stat(filepath.Join(dir, "summary.json")); err == nil {
					continue
				}
				start := time.Now()
				s := run(*tier, e, dir, *work)
				status := fmt.Sprintf("%d bare, %d contextual", s.Total.Bare, s.Total.Contextual)
				if s.Error != "" {
					status = "error: " + s.Error
				}
				log.Printf("%s: %s (%s)", e.Repo, status, time.Since(start).Round(time.Second))
			}
		}()
	}
	for _, e := range entries {
		ch <- e
	}
	close(ch)
	wg.Wait()
}

// readCorpus reads the repo and commit columns of a corpus TSV file.
func readCorpus(name string) ([]Entry, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	header := strings.Split(lines[0], "\t")
	repoCol, commitCol := -1, -1
	for i, h := range header {
		switch h {
		case "repo":
			repoCol = i
		case "commit":
			commitCol = i
		}
	}
	if repoCol < 0 || commitCol < 0 {
		return nil, fmt.Errorf("%s: missing repo or commit column", name)
	}
	var entries []Entry
	for _, line := range lines[1:] {
		f := strings.Split(line, "\t")
		entries = append(entries, Entry{Repo: f[repoCol], Commit: f[commitCol]})
	}
	return entries, nil
}

// run fetches, analyses and writes the results for one repository. Errors
// are recorded in the summary so that they are visible in the results.
func run(tier string, e Entry, dir, work string) RepoSummary {
	s := RepoSummary{Tier: tier, Repo: e.Repo, Commit: e.Commit}
	res, err := analyze(e, work)
	if err != nil {
		s.Error = err.Error()
	} else {
		s.Summary = res.summary
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	if err == nil {
		if err := writeJSONL(filepath.Join(dir, "checks.jsonl.gz"), res.checks); err != nil {
			log.Fatal(err)
		}
		if err := writeJSONL(filepath.Join(dir, "unresolved.jsonl.gz"), res.unresolved); err != nil {
			log.Fatal(err)
		}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	// Written last: its presence marks the repository as done.
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), append(data, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	return s
}

type result struct {
	summary    checks.Summary
	checks     []checks.Check
	unresolved []checks.Unresolved
}

func analyze(e Entry, work string) (*result, error) {
	tmp, err := os.MkdirTemp(work, "survey-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := fetch(e, tmp); err != nil {
		return nil, err
	}
	m, err := checks.Load(tmp, "github.com/"+e.Repo)
	if err != nil {
		return nil, err
	}
	res := m.Analyze()
	return &result{
		summary:    checks.Summarize(m, res),
		checks:     res.Checks,
		unresolved: res.Unresolved,
	}, nil
}

// fetch downloads the GitHub tarball for the entry and extracts the .go and
// go.mod files into dst, stripping the top-level directory.
func fetch(e Entry, dst string) error {
	url := fmt.Sprintf("https://codeload.github.com/%s/tar.gz/%s", e.Repo, e.Commit)
	var resp *http.Response
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 10 * time.Second)
		}
		resp, err = http.Get(url)
		if err != nil {
			continue
		}
		if resp.StatusCode == http.StatusOK {
			break
		}
		resp.Body.Close()
		err = fmt.Errorf("GET %s: %s", url, resp.Status)
		if resp.StatusCode == http.StatusNotFound {
			return err
		}
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := hdr.Name
		base := path.Base(name)
		if !strings.HasSuffix(base, ".go") && base != "go.mod" {
			continue
		}
		// Strip the "<repo>-<commit>/" prefix.
		_, rel, ok := strings.Cut(name, "/")
		if !ok || rel == "" || strings.Contains(rel, "..") {
			continue
		}
		p := filepath.Join(dst, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		f, err := os.Create(p)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, tr)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
}

func writeJSONL[T any](name string, items []T) error {
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	enc := json.NewEncoder(gz)
	for _, it := range items {
		if err := enc.Encode(it); err != nil {
			f.Close()
			return err
		}
	}
	if err := gz.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
