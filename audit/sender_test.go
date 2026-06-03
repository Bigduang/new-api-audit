package audit

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func useTestConfig(c config, q chan outboundEvent) {
	initOnce = sync.Once{}
	initOnce.Do(func() {
		cfg = c
		queue = q
	})
}

func TestEnqueueDropsWhenQueueIsFull(t *testing.T) {
	q := make(chan outboundEvent, 1)
	useTestConfig(config{enabled: true, excluded: map[string]struct{}{}}, q)

	EnqueueUsage(UsageEvent{RequestId: "req-1"})

	done := make(chan struct{})
	go func() {
		EnqueueUsage(UsageEvent{RequestId: "req-2"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("enqueue blocked when queue was full")
	}
}

func TestEnqueueDropsRequestWithoutRequestId(t *testing.T) {
	q := make(chan outboundEvent, 1)
	useTestConfig(config{enabled: true, excluded: map[string]struct{}{}}, q)

	EnqueueRequest(RequestEvent{RequestId: " \t\n", PromptText: "hello"})

	select {
	case event := <-q:
		t.Fatalf("expected request without request_id to be dropped, got %s", event.path)
	default:
	}
}

func TestEnqueueDropsUsageWithoutRequestId(t *testing.T) {
	q := make(chan outboundEvent, 1)
	useTestConfig(config{enabled: true, excluded: map[string]struct{}{}}, q)

	EnqueueUsage(UsageEvent{RequestId: ""})

	select {
	case event := <-q:
		t.Fatalf("expected usage without request_id to be dropped, got %s", event.path)
	default:
	}
}

func TestEnqueueDropsOversizedEvent(t *testing.T) {
	q := make(chan outboundEvent, 1)
	useTestConfig(config{enabled: true, maxEventBytes: 64, excluded: map[string]struct{}{}}, q)

	EnqueueRequest(RequestEvent{
		RequestId:  "req-large",
		PromptText: "this prompt is intentionally long enough to exceed the tiny test limit",
	})

	select {
	case event := <-q:
		t.Fatalf("expected oversized event to be dropped, got %s", event.path)
	default:
	}
}

func TestEnqueueCompactsOversizedRequestEvent(t *testing.T) {
	q := make(chan outboundEvent, 1)
	useTestConfig(config{enabled: true, maxEventBytes: 1200, excluded: map[string]struct{}{}}, q)

	largePrompt := strings.Repeat("x", 5000)
	EnqueueRequest(RequestEvent{
		RequestId:     "req-large",
		UserId:        7,
		Username:      "alice",
		TokenId:       11,
		TokenName:     "coding",
		ModelName:     "gpt-test",
		PromptHash:    HashText(largePrompt),
		PromptPreview: PreviewText(largePrompt, 500),
		PromptText:    largePrompt,
	})

	select {
	case event := <-q:
		if event.path != requestPath {
			t.Fatalf("unexpected path: %s", event.path)
		}
		if len(event.body) > 1200 {
			t.Fatalf("compacted event is still too large: %d", len(event.body))
		}
		var payload RequestEvent
		if err := json.Unmarshal(event.body, &payload); err != nil {
			t.Fatalf("failed to unmarshal compacted event: %v", err)
		}
		if !payload.PromptOmitted {
			t.Fatal("expected prompt_omitted=true")
		}
		if payload.PromptText != "" {
			t.Fatal("expected prompt_text to be omitted")
		}
		if payload.PromptLen != 5000 {
			t.Fatalf("unexpected prompt_len: %d", payload.PromptLen)
		}
		if payload.PromptPreview == "" || payload.PromptHash == "" {
			t.Fatal("expected preview and hash to be preserved")
		}
	default:
		t.Fatal("expected compacted request event to be queued")
	}
}

func TestPreviewTextDoesNotSplitMultibyteCharacters(t *testing.T) {
	preview := PreviewText("你好 世界 测试", 4)
	if !utf8.ValidString(preview) {
		t.Fatalf("preview is not valid utf-8: %q", preview)
	}
	if preview != "你好 世..." {
		t.Fatalf("unexpected preview: %q", preview)
	}
}
