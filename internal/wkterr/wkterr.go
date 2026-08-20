package wkterr

import (
	"encoding/json"
	"fmt"
	"strings"
)

type E struct {
	Code     string    `json:"code"`
	Message  string    `json:"message"`
	Repo     string    `json:"repo,omitempty"`
	Path     string    `json:"path,omitempty"`
	Expected string    `json:"expected,omitempty"`
	Found    string    `json:"found,omitempty"`
	Problems []Problem `json:"problems,omitempty"`
	Remedy   []string  `json:"remedy,omitempty"`
}

// Problem is one thing standing in the way, as opposed to Remedy, which is
// what to do about it. Keeping them apart is the whole point: a refusal that
// lists its blockers under "remedy" tells the user what is wrong twice and
// what to do never.
type Problem struct {
	Code   string
	Repo   string
	Path   string
	Detail string
	// Info marks a problem that is reported but does not block. The zero
	// value therefore blocks, so a caller who forgets the field fails
	// closed — the same rule the teardown checks follow.
	Info bool
}

// MarshalJSON writes the positive form ("blocking": true) while the Go field
// stays the negative one, so the zero value keeps failing closed.
func (p Problem) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Code     string `json:"code"`
		Repo     string `json:"repo,omitempty"`
		Path     string `json:"path,omitempty"`
		Detail   string `json:"detail,omitempty"`
		Blocking bool   `json:"blocking"`
	}{p.Code, p.Repo, p.Path, p.Detail, !p.Info})
}

// UnmarshalJSON is the inverse, so a caller can round-trip an error.
func (p *Problem) UnmarshalJSON(b []byte) error {
	var raw struct {
		Code     string `json:"code"`
		Repo     string `json:"repo,omitempty"`
		Path     string `json:"path,omitempty"`
		Detail   string `json:"detail,omitempty"`
		Blocking bool   `json:"blocking"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*p = Problem{Code: raw.Code, Repo: raw.Repo, Path: raw.Path, Detail: raw.Detail, Info: !raw.Blocking}
	return nil
}

func New(code, msg string) *E { return &E{Code: code, Message: msg} }

func (e *E) Error() string {
	if e.Found != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.Found)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *E) WithRepo(r string) *E      { e.Repo = r; return e }
func (e *E) WithPath(p string) *E      { e.Path = p; return e }
func (e *E) WithFound(f string) *E     { e.Found = f; return e }
func (e *E) WithExpected(x string) *E  { e.Expected = x; return e }
func (e *E) WithRemedy(r ...string) *E { e.Remedy = append(e.Remedy, r...); return e }

// WithProblem records one blocker. Detail is flattened to a single line here
// rather than at every call site, because the callers feed it git output.
func (e *E) WithProblem(p Problem) *E {
	p.Detail = strings.Join(strings.Fields(p.Detail), " ")
	e.Problems = append(e.Problems, p)
	return e
}

func JSON(err error) []byte {
	if err == nil {
		b, _ := json.Marshal(&E{Code: "WKT_INTERNAL", Message: ""})
		return b
	}
	if e, ok := err.(*E); ok {
		b, _ := json.Marshal(e)
		return b
	}
	b, _ := json.Marshal(&E{Code: "WKT_INTERNAL", Message: err.Error()})
	return b
}
