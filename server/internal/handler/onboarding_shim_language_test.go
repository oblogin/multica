package handler

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestNoRuntimeIssueDescriptionUsesRussian(t *testing.T) {
	for _, language := range []string{"ru", "ru-RU"} {
		description := noRuntimeIssueDescription(pgtype.Text{String: language, Valid: true})
		if !strings.Contains(description, "Добро пожаловать в Multica") {
			t.Errorf("language %q did not select Russian issue copy", language)
		}
		if strings.Contains(description, "Welcome to Multica") {
			t.Errorf("language %q fell back to English issue copy", language)
		}
	}
}
