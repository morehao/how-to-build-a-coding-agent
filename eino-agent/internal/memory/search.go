package memory

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/session"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

// Hit is one retrieval result from the plan or a saved session.
type Hit struct {
	Source    string
	SessionID string
	Role      string
	When      time.Time
	Snippet   string
	Score     int
}

// Search does a small, deterministic keyword search over the working-memory
// plan and recent session snapshots. It is not embeddings or a vector store:
// the goal is to make prior decisions retrievable without loading every
// session into the model context.
func Search(ws *workspace.Workspace, sessionDir, query string, limit int) ([]Hit, error) {
	if ws == nil {
		return nil, fmt.Errorf("memory: workspace is required")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("memory: query is empty")
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}
	terms := splitTerms(query)
	if len(terms) == 0 {
		return nil, fmt.Errorf("memory: query has no searchable terms")
	}

	var hits []Hit
	if plan, err := Load(ws); err == nil && strings.TrimSpace(plan) != "" {
		if score, snippet := match(plan, terms); score > 0 {
			hits = append(hits, Hit{Source: RelativePath, Role: "plan", Snippet: snippet, Score: score})
		}
	}

	if strings.TrimSpace(sessionDir) != "" {
		infos, err := session.List(sessionDir)
		if err != nil {
			return nil, fmt.Errorf("memory: list sessions: %w", err)
		}
		scanned := 0
		for _, info := range infos {
			if info.Error != "" {
				continue
			}
			if info.Workspace != "" && info.Workspace != ws.Root() {
				continue
			}
			if scanned >= 50 {
				break
			}
			scanned++
			s, err := session.Load(sessionDir, info.ID)
			if err != nil {
				continue
			}
			if score, snippet := match(s.Summary, terms); score > 0 {
				hits = append(hits, Hit{
					Source: "session", SessionID: s.ID, Role: "summary", When: s.UpdatedAt,
					Snippet: snippet, Score: score,
				})
			}
			for _, m := range s.Messages {
				if score, snippet := match(m.Content, terms); score > 0 {
					hits = append(hits, Hit{
						Source: "session", SessionID: s.ID, Role: m.Role, When: m.At,
						Snippet: snippet, Score: score,
					})
				}
			}
		}
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].When.After(hits[j].When)
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func splitTerms(q string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, f := range strings.Fields(strings.ToLower(q)) {
		f = strings.Trim(f, `"'.,:;!?()[]{}<>`)
		if len(f) < 2 {
			continue
		}
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	return out
}

func match(text string, terms []string) (int, string) {
	if strings.TrimSpace(text) == "" {
		return 0, ""
	}
	lower := strings.ToLower(text)
	score := 0
	first := -1
	for _, term := range terms {
		n := strings.Count(lower, term)
		if n == 0 {
			continue
		}
		score += n
		if first < 0 {
			first = strings.Index(lower, term)
		}
	}
	if score == 0 {
		return 0, ""
	}
	if first < 0 {
		first = 0
	}
	runes := []rune(text)
	firstRune := len([]rune(text[:first]))
	start := firstRune - 100
	if start < 0 {
		start = 0
	}
	end := firstRune + 180
	if end > len(runes) {
		end = len(runes)
	}
	snippet := strings.Join(strings.Fields(string(runes[start:end])), " ")
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(runes) {
		snippet += "..."
	}
	return score, snippet
}
