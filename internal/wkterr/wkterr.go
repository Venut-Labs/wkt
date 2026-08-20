package wkterr

import (
	"encoding/json"
	"fmt"
)

type E struct {
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Repo     string   `json:"repo,omitempty"`
	Path     string   `json:"path,omitempty"`
	Expected string   `json:"expected,omitempty"`
	Found    string   `json:"found,omitempty"`
	Remedy   []string `json:"remedy,omitempty"`
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
