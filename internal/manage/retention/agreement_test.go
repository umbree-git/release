package retention_test

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"umbree-release-r2-mirror/prune"

	"github.com/umbree-git/release/internal/manage/retention"
)

const (
	countsBegin = "<!-- retention-counts:begin -->"
	countsEnd   = "<!-- retention-counts:end -->"
)

var countRow = regexp.MustCompile(`^\| ([A-Za-z ]+?) \| (production|beta) \| ([0-9]+) \|$`)

func readmeCounts(t *testing.T) map[string]int {
	t.Helper()
	body, err := os.ReadFile("../../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Count(text, countsBegin) != 1 || strings.Count(text, countsEnd) != 1 {
		t.Fatalf("README.md must hold exactly one %s … %s block", countsBegin, countsEnd)
	}
	block := text[strings.Index(text, countsBegin):strings.Index(text, countsEnd)]
	counts := map[string]int{}
	for _, line := range strings.Split(block, "\n") {
		m := countRow.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[3])
		counts[m[1]+" "+m[2]] = n
	}
	return counts
}

func scriptDefault(t *testing.T, name string) int {
	t.Helper()
	body, err := os.ReadFile("../../../tools/prune-releases.sh")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^`+name+`=([0-9]+)$`).FindAllStringSubmatch(string(body), -1)
	if len(m) != 1 {
		t.Fatalf("prune-releases.sh sets %s %d times, want once", name, len(m))
	}
	n, _ := strconv.Atoi(m[0][1])
	return n
}

func TestRetentionCountsAgree(t *testing.T) {
	readme := readmeCounts(t)
	want := map[string]int{
		"Public surface production": 5,
		"Public surface beta":       1,
		"Gated store production":    3,
	}
	if len(readme) != len(want) {
		t.Fatalf("the README's counts block holds %v, want exactly the rows %v", readme, want)
	}
	for k, n := range want {
		if readme[k] != n {
			t.Fatalf("README %q = %d, want %d", k, readme[k], n)
		}
	}
	for name, pair := range map[string][2]int{
		"retention.KeepPublicProduction":        {retention.KeepPublicProduction, readme["Public surface production"]},
		"retention.KeepGated":                   {retention.KeepGated, readme["Gated store production"]},
		"prune.DefaultKeepStable":               {prune.DefaultKeepStable, readme["Public surface production"]},
		"prune.DefaultKeepBeta":                 {prune.DefaultKeepBeta, readme["Public surface beta"]},
		"prune-releases.sh KEEP_STABLE_DEFAULT": {scriptDefault(t, "KEEP_STABLE_DEFAULT"), readme["Public surface production"]},
		"prune-releases.sh KEEP_BETA_DEFAULT":   {scriptDefault(t, "KEEP_BETA_DEFAULT"), readme["Public surface beta"]},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %d, the README says %d", name, pair[0], pair[1])
		}
	}
}
