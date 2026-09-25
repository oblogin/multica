package daemon

import (
	"strings"
	"testing"
)

func TestClarificationContextNamesNewRunAndQuotesAnswer(t *testing.T) {
	prompt := BuildPrompt(Task{
		IssueID: "issue-1",
		InteractionContext: `interaction_id=i1 source_task_id=old answered_by=u1 answered_at=2026-09-25T00:00:00Z possibly_delivered=false
questions="[{\"question\":\"Which scope?\"}]"
answer="{\"answer\":\"A\"}"`,
	}, "codex")
	for _, part := range []string{"[CLARIFICATION]", "NEW run", "original process was not resumed", `answer="{\"answer\":\"A\"}"`} {
		if !strings.Contains(prompt, part) {
			t.Fatalf("prompt missing %q", part)
		}
	}
}
