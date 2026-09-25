package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var taskCmd = &cobra.Command{Use: "task", Short: "Actions for the current agent run"}

var taskAskCmd = &cobra.Command{
	Use:   "ask <question>",
	Short: "Save a question and finish this run with needs_input",
	Long:  "Save a question for workspace members. This command returns immediately; an answer can only continue work in a new run. It never supplies an answer to the current process.",
	Args:  exactArgs(1),
	RunE:  runTaskAsk,
}

func init() {
	taskCmd.AddCommand(taskAskCmd)
	taskAskCmd.Flags().StringSlice("option", nil, "Allowed answer (repeat for a choice question)")
	taskAskCmd.Flags().String("request-id", "", "UUID for safe retries of this request")
}

func runTaskAsk(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	if client.TaskID == "" || !strings.HasPrefix(client.Token, cli.TaskTokenPrefix) {
		return fmt.Errorf("task ask requires the current agent task token and MULTICA_TASK_ID")
	}
	question := strings.TrimSpace(args[0])
	if question == "" || len(question) > 2000 {
		return fmt.Errorf("question must contain 1–2000 bytes")
	}
	options, _ := cmd.Flags().GetStringSlice("option")
	if len(options) > 10 {
		return fmt.Errorf("at most 10 options are allowed")
	}
	for _, option := range options {
		if strings.TrimSpace(option) == "" || len(option) > 500 {
			return fmt.Errorf("options must contain 1–500 bytes")
		}
	}
	requestID, _ := cmd.Flags().GetString("request-id")
	if requestID == "" {
		requestID = uuid.NewString()
	}
	if _, err := uuid.Parse(requestID); err != nil {
		return fmt.Errorf("request-id must be a UUID")
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var result map[string]any
	err = client.PostJSON(ctx, "/api/tasks/"+client.TaskID+"/interactions", map[string]any{
		"client_request_id": requestID,
		"questions":         []map[string]any{{"id": "answer", "question": question, "options": options}},
	}, &result)
	if err != nil {
		return taskAskSaveError(err)
	}
	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, result)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Question saved: %v\nFinish this run with needs_input. An answer will continue in a new run.\n", result["id"])
	return nil
}

func taskAskSaveError(err error) error {
	var httpErr *cli.HTTPError
	if errors.As(err, &httpErr) && (httpErr.StatusCode == http.StatusNotFound || httpErr.StatusCode == http.StatusMethodNotAllowed) {
		return fmt.Errorf("this server does not support task questions; the question was not saved")
	}
	return fmt.Errorf("save task question: %w", err)
}
