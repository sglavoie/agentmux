package aslbridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestSecretTextNeverReachesAFormatter(t *testing.T) {
	const answer = "the passphrase is hunter2"
	body := QuestionBody{PlanDir: "/p", QuestionID: "q1", Body: SecretText(answer)}

	for _, rendered := range []string{
		fmt.Sprintf("%v", body),
		fmt.Sprintf("%+v", body),
		fmt.Sprintf("%#v", body),
		fmt.Sprint(body.Body),
		fmt.Sprintf("%q", body.Body.String()),
		body.Body.String(),
	} {
		if strings.Contains(rendered, "hunter2") {
			t.Fatalf("a formatted question body leaked its text: %s", rendered)
		}
	}

	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("question", "body", body.Body, "request", ActionRequest{Text: body.Body})
	if strings.Contains(buf.String(), "hunter2") {
		t.Fatalf("a log line leaked the question body: %s", buf.String())
	}
}

func TestSecretTextStillTravelsOnTheWire(t *testing.T) {
	const answer = "yes, proceed"
	data, err := json.Marshal(map[string]any{"text": SecretText(answer)})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if !strings.Contains(string(data), answer) {
		t.Fatalf("the protocol payload must carry the real text, got %s", data)
	}
	if SecretText(answer).Reveal() != answer {
		t.Fatal("Reveal must return the underlying text")
	}
}

func TestOutboundFrameCeilingIsEnforcedOnWrite(t *testing.T) {
	c := &conn{stdin: nopWriteCloser{}, pending: map[string]chan result{}}
	oversize := strings.Repeat("x", MaxFrameBytes)
	err := c.write(newOutbound("1", MethodQuestionReply, map[string]any{"text": oversize}))
	if err == nil || !strings.Contains(err.Error(), "frame ceiling") {
		t.Fatalf("write error = %v, want the frame ceiling to reject it", err)
	}
}

type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }
