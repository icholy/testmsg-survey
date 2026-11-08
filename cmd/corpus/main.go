// Command corpus builds the pinned repository lists for each corpus tier.
//
// Usage:
//
//	GITHUB_TOKEN=$(gh auth token) go run ./cmd/corpus go      > corpus/go.tsv
//	GITHUB_TOKEN=$(gh auth token) go run ./cmd/corpus -exclude corpus/go.tsv popular > corpus/popular.tsv
//
// Each tier is written as TSV with a header row. Every entry is pinned to the
// commit at the head of the repository's default branch at the time of the run.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	log.SetFlags(0)
	n := flag.Int("n", 500, "number of repositories in the popular tier")
	exclude := flag.String("exclude", "", "TSV file whose first column lists repositories to leave out of the popular tier")
	flag.Parse()
	gh := &GitHub{Token: os.Getenv("GITHUB_TOKEN")}
	if gh.Token == "" {
		log.Fatal("GITHUB_TOKEN must be set")
	}
	var err error
	switch flag.Arg(0) {
	case "go":
		err = goTier(gh)
	case "popular":
		var skip map[string]bool
		skip, err = readExclude(*exclude)
		if err == nil {
			err = popularTier(gh, *n, skip)
		}
	default:
		log.Fatalf("usage: corpus [-n N] go|popular")
	}
	if err != nil {
		log.Fatal(err)
	}
}

// Repo is the subset of the GitHub repository object that we use.
type Repo struct {
	FullName      string `json:"full_name"`
	Fork          bool   `json:"fork"`
	Archived      bool   `json:"archived"`
	DefaultBranch string `json:"default_branch"`
	Stars         int    `json:"stargazers_count"`
}

// goTier lists golang/go and the golang.org/x/... repositories. A repository
// in the golang GitHub organisation counts as an x repository if the go.mod at
// its root declares a golang.org/x/ module path.
func goTier(gh *GitHub) error {
	var repos []Repo
	for page := 1; ; page++ {
		var batch []Repo
		path := fmt.Sprintf("/orgs/golang/repos?type=sources&per_page=100&page=%d", page)
		if err := gh.JSON(path, &batch); err != nil {
			return err
		}
		repos = append(repos, batch...)
		if len(batch) < 100 {
			break
		}
	}
	fmt.Println("repo\tmodule\tcommit")
	for _, r := range repos {
		if r.Fork || r.Archived {
			continue
		}
		var module string
		if r.FullName == "golang/go" {
			module = "std"
		} else {
			gomod, err := gh.Raw(fmt.Sprintf("/repos/%s/contents/go.mod?ref=%s", r.FullName, r.DefaultBranch))
			if err != nil {
				log.Printf("skip %s: no go.mod", r.FullName)
				continue
			}
			module = modulePath(gomod)
			if !strings.HasPrefix(module, "golang.org/x/") {
				log.Printf("skip %s: module %q", r.FullName, module)
				continue
			}
		}
		sha, err := gh.HeadCommit(r)
		if err != nil {
			return err
		}
		fmt.Printf("%s\t%s\t%s\n", r.FullName, module, sha)
	}
	return nil
}

// popularTier lists the n most-starred Go repositories, excluding forks,
// archived repositories and the repositories in skip (the Go project tier).
func popularTier(gh *GitHub, n int, skip map[string]bool) error {
	fmt.Println("rank\trepo\tstars\tcommit")
	seen := map[string]bool{}
	rank := 0
	// The search API returns at most 1000 results (10 pages of 100).
	for page := 1; page <= 10 && rank < n; page++ {
		var res struct {
			Items []Repo `json:"items"`
		}
		path := fmt.Sprintf("/search/repositories?q=%s&sort=stars&order=desc&per_page=100&page=%d",
			"language:Go+fork:false+archived:false", page)
		if err := gh.JSON(path, &res); err != nil {
			return err
		}
		if len(res.Items) == 0 {
			break
		}
		for _, r := range res.Items {
			if rank >= n {
				break
			}
			if r.Fork || r.Archived || seen[r.FullName] {
				continue
			}
			seen[r.FullName] = true
			if skip[strings.ToLower(r.FullName)] {
				log.Printf("skip %s: excluded", r.FullName)
				continue
			}
			sha, err := gh.HeadCommit(r)
			if err != nil {
				log.Printf("skip %s: %v", r.FullName, err)
				continue
			}
			rank++
			fmt.Printf("%d\t%s\t%d\t%s\n", rank, r.FullName, r.Stars, sha)
		}
	}
	if rank < n {
		return fmt.Errorf("only found %d of %d repositories", rank, n)
	}
	return nil
}

// readExclude reads the first column of a TSV file, skipping the header row.
func readExclude(path string) (map[string]bool, error) {
	skip := map[string]bool{}
	if path == "" {
		return skip, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for _, line := range lines[1:] {
		repo, _, _ := strings.Cut(line, "\t")
		skip[strings.ToLower(repo)] = true
	}
	return skip, nil
}

// modulePath returns the module path declared in a go.mod file.
func modulePath(gomod []byte) string {
	for _, line := range strings.Split(string(gomod), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" {
			return strings.Trim(fields[1], `"`)
		}
	}
	return ""
}

// GitHub is a minimal GitHub REST API client.
type GitHub struct {
	Token string
}

func (gh *GitHub) do(path, accept string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequest("GET", "https://api.github.com"+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+gh.Token)
		req.Header.Set("Accept", accept)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		switch {
		case resp.StatusCode == http.StatusOK:
			return body, nil
		case (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) && attempt < 5:
			// Secondary rate limit, mostly from the search API.
			time.Sleep(time.Duration(attempt+1) * 15 * time.Second)
		default:
			return nil, fmt.Errorf("GET %s: %s: %s", path, resp.Status, body)
		}
	}
}

// JSON decodes the response for path into v.
func (gh *GitHub) JSON(path string, v any) error {
	body, err := gh.do(path, "application/vnd.github+json")
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

// Raw returns the raw content of a file from the contents API.
func (gh *GitHub) Raw(path string) ([]byte, error) {
	return gh.do(path, "application/vnd.github.raw+json")
}

// HeadCommit returns the SHA of the head of the repository's default branch.
func (gh *GitHub) HeadCommit(r Repo) (string, error) {
	sha, err := gh.do(fmt.Sprintf("/repos/%s/commits/%s", r.FullName, r.DefaultBranch), "application/vnd.github.sha")
	return strings.TrimSpace(string(sha)), err
}
