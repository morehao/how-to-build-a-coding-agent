package trace

import (
	"strings"
	"testing"
)

func TestSummarize(t *testing.T) {
	raw := `{"ts":"2026-01-01T00:00:00Z","type":"turn_start"}
{"ts":"2026-01-01T00:00:01Z","type":"model_end","duration_ms":120,"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}
{"ts":"2026-01-01T00:00:02Z","type":"tool_end","duration_ms":30,"tool":"read_file","result":"ok"}
{"ts":"2026-01-01T00:00:03Z","type":"tool_end","duration_ms":5,"tool":"bash","error":"boom"}
{"ts":"2026-01-01T00:00:04Z","type":"turn_end","duration_ms":200,"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5},"error":"final error"}
not-json
`
	st, err := Summarize(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if st.Events != 5 || st.Turns != 1 || st.ModelCalls != 1 || st.ToolCalls != 2 || st.ToolErrors != 1 {
		t.Fatalf("stats = %+v", st)
	}
	if st.PromptTokens != 12 || st.CompletionTokens != 7 || st.TotalTokens != 19 {
		t.Fatalf("tokens = %+v", st)
	}
	if st.ModelMS != 120 || st.ToolMS != 35 {
		t.Fatalf("durations = %+v", st)
	}
	if len(st.Errors) != 2 || !strings.Contains(st.String(), "final error") {
		t.Fatalf("errors = %+v\n%s", st.Errors, st.String())
	}
}

func TestSummarizeEmpty(t *testing.T) {
	st, err := Summarize(strings.NewReader(""))
	if err != nil || st.Events != 0 || !strings.Contains(st.String(), "no events") {
		t.Fatalf("st=%+v err=%v", st, err)
	}
}
