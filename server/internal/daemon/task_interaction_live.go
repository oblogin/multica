package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type liveQuestion struct {
	ID       string   `json:"id"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

type claudeQuestionInput struct {
	Questions []struct {
		Question    string `json:"question"`
		MultiSelect bool   `json:"multiSelect"`
		Options     []struct {
			Label string `json:"label"`
		} `json:"options"`
	} `json:"questions"`
}

func parseClaudeQuestions(raw json.RawMessage) ([]liveQuestion, []string, error) {
	var input claudeQuestionInput
	if err := json.Unmarshal(raw, &input); err != nil || len(input.Questions) == 0 || len(input.Questions) > 5 {
		return nil, nil, errors.New("invalid Claude questions")
	}
	questions := make([]liveQuestion, 0, len(input.Questions))
	texts := make([]string, 0, len(input.Questions))
	seen := make(map[string]bool)
	for i, q := range input.Questions {
		if q.MultiSelect {
			return nil, nil, errors.New("multi-select questions require multica task ask")
		}
		if strings.TrimSpace(q.Question) == "" || seen[q.Question] {
			return nil, nil, errors.New("empty or duplicate Claude question")
		}
		seen[q.Question] = true
		question := liveQuestion{ID: fmt.Sprintf("q%d", i), Question: q.Question}
		for _, option := range q.Options {
			question.Options = append(question.Options, option.Label)
		}
		questions = append(questions, question)
		texts = append(texts, q.Question)
	}
	return questions, texts, nil
}

type liveQuestionBinding struct {
	RuntimeID    string `json:"runtime_id"`
	DispatchedAt string `json:"dispatched_at"`
	ProcessNonce string `json:"process_nonce"`
}

type liveQuestionSession struct {
	client   *Client
	taskID   string
	binding  liveQuestionBinding
	mu       sync.Mutex
	requests map[string]string
}

func newLiveQuestionSession(client *Client, task Task) *liveQuestionSession {
	return &liveQuestionSession{
		client: client, taskID: task.ID,
		binding:  liveQuestionBinding{RuntimeID: task.RuntimeID, DispatchedAt: task.DispatchedAt, ProcessNonce: uuid.NewString()},
		requests: make(map[string]string),
	}
}

func (s *liveQuestionSession) requestBody(requestID string, questions []liveQuestion) map[string]any {
	requestKey := fmt.Sprintf("%s:%s", s.binding.ProcessNonce, requestID)
	if len(requestKey) > 200 {
		requestKey = fmt.Sprintf("%s:%x", s.binding.ProcessNonce, sha256.Sum256([]byte(requestID)))
	}
	body := map[string]any{
		"runtime_id": s.binding.RuntimeID, "dispatched_at": s.binding.DispatchedAt,
		"process_nonce": s.binding.ProcessNonce, "provider_request_id": requestKey,
	}
	if questions != nil {
		body["questions"] = questions
	}
	return body
}

func (s *liveQuestionSession) resolve(ctx context.Context, requestID string, input json.RawMessage) (map[string]string, error) {
	questions, texts, err := parseClaudeQuestions(input)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/api/daemon/tasks/%s/interactions", s.taskID)
	var created struct {
		ID string `json:"id"`
	}
	for {
		err = s.client.postJSON(ctx, path, s.requestBody(requestID, questions), &created)
		if err == nil {
			break
		}
		var requestErr *requestError
		if errors.As(err, &requestErr) && requestErr.StatusCode < 500 {
			return nil, err
		}
		if !waitLiveQuestionPoll(ctx) {
			return nil, ctx.Err()
		}
	}
	if created.ID == "" {
		return nil, errors.New("live question response has no id")
	}
	s.mu.Lock()
	s.requests[requestID] = created.ID
	s.mu.Unlock()
	claimPath := path + "/" + created.ID + "/claim"
	for {
		var result struct {
			Answer map[string]string `json:"answer"`
		}
		var pending bool
		decode := responseDecoder(func(r io.Reader) error {
			data, err := io.ReadAll(io.LimitReader(r, 32<<10))
			if err != nil {
				return err
			}
			if len(data) == 0 {
				pending = true
				return nil
			}
			return json.Unmarshal(data, &result)
		})
		err = s.client.postJSON(ctx, claimPath, s.requestBody(requestID, nil), decode)
		if err == nil && !pending {
			if len(result.Answer) != len(questions) {
				return nil, errors.New("incomplete live answer")
			}
			answers := make(map[string]string, len(questions))
			for i, q := range questions {
				answer := result.Answer[q.ID]
				if strings.TrimSpace(answer) == "" {
					return nil, errors.New("empty live answer")
				}
				answers[texts[i]] = answer
			}
			return answers, nil
		}
		var requestErr *requestError
		if err != nil && errors.As(err, &requestErr) && requestErr.StatusCode < 500 {
			return nil, err
		}
		if !waitLiveQuestionPoll(ctx) {
			return nil, ctx.Err()
		}
	}
}

func (s *liveQuestionSession) ack(ctx context.Context, requestID string) error {
	s.mu.Lock()
	id := s.requests[requestID]
	s.mu.Unlock()
	if id == "" {
		return errors.New("live question was not created")
	}
	path := fmt.Sprintf("/api/daemon/tasks/%s/interactions/%s/ack", s.taskID, id)
	return s.client.postJSONWithRetry(ctx, path, s.requestBody(requestID, nil), nil,
		[]time.Duration{0, 100 * time.Millisecond, 300 * time.Millisecond})
}

func waitLiveQuestionPoll(ctx context.Context) bool {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
