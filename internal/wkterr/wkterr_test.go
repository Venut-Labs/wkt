package wkterr

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProblemsAreCarriedSeparatelyFromRemedy covers adversarial finding F5.
// A refusal has two different things to say — what is in the way, and what to
// do about it — and folding the first into the second left "remedy" holding a
// list of problems and no action at all.
func TestProblemsAreCarriedSeparatelyFromRemedy(t *testing.T) {
	e := New("WKT_WOULD_LOSE_WORK", "removal would lose work").
		WithProblem(Problem{Code: "WKT_DIRTY", Repo: "svc-a", Detail: "2 modified paths"}).
		WithProblem(Problem{Code: "WKT_REGENERABLE_IGNORED", Repo: "svc-a", Path: "dist/", Info: true}).
		WithRemedy("commit or stash the changes, then retry")

	var got struct {
		Problems []Problem `json:"problems"`
		Remedy   []string  `json:"remedy"`
	}
	if err := json.Unmarshal(JSON(e), &got); err != nil {
		t.Fatalf("error JSON must parse: %v", err)
	}
	if len(got.Problems) != 2 {
		t.Fatalf("want both problems, got %+v", got.Problems)
	}
	if got.Problems[0].Info {
		t.Fatal("a problem blocks unless it says otherwise — the zero value must fail closed")
	}
	if !got.Problems[1].Info {
		t.Fatal("an informational problem must not read as blocking")
	}
	if len(got.Remedy) != 1 || strings.Contains(got.Remedy[0], "WKT_") {
		t.Fatalf("remedy must hold actions, not problem codes: %v", got.Remedy)
	}
}

// TestErrorStaysOneLine pins the constraint the whole type exists for: no
// error surface may span lines, because raw git stderr must never reach it.
func TestErrorStaysOneLine(t *testing.T) {
	e := New("WKT_X", "boom").WithProblem(Problem{Code: "WKT_DIRTY", Detail: "a\nb"})
	if strings.Contains(e.Error(), "\n") {
		t.Fatalf("Error() must stay single-line, got %q", e.Error())
	}
}
