package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ─── Fake service ─────────────────────────────────────────────────────────────

type fakeIntakeProcessor struct {
	calls int
	input *IntakeInput
	err   error
}

func (f *fakeIntakeProcessor) ProcessMessages(_ context.Context, input *IntakeInput) error {
	f.calls++
	f.input = input
	return f.err
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

const testInternalToken = "internal-token-secret-abc"

func buildInternalHandler(svc IntakeProcessor, token string) *InternalHandler {
	return NewInternalHandler(svc, token, 65536, zap.NewNop())
}

func payloadWithMessages(phoneNumberID, msgID, from string) []byte {
	p := MetaWebhookPayload{
		Object: "whatsapp_business_account",
		Entry: []MetaEntry{
			{
				ID: "entry-001",
				Changes: []MetaChange{
					{
						Field: "messages",
						Value: MetaChangeValue{
							MessagingProduct: "whatsapp",
							Metadata: MetaMetadata{
								PhoneNumberID:      phoneNumberID,
								DisplayPhoneNumber: "15550001234",
							},
							Messages: []MetaMessage{
								{ID: msgID, From: from, Timestamp: "1715000000", Type: "text"},
							},
						},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(p)
	return b
}

func payloadWithNoMessages() []byte {
	p := MetaWebhookPayload{
		Object: "whatsapp_business_account",
		Entry: []MetaEntry{
			{
				ID: "entry-001",
				Changes: []MetaChange{
					{
						Field: "messages",
						Value: MetaChangeValue{
							MessagingProduct: "whatsapp",
							Metadata: MetaMetadata{
								PhoneNumberID: "phone-001",
							},
							Messages: nil,
						},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(p)
	return b
}

func doIntake(h *InternalHandler, body []byte, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/internal/whatsapp/meta/messages/intake", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-EDN-Internal-Token", token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// ─── Token auth tests ─────────────────────────────────────────────────────────

func TestInternalHandler_WrongToken_Returns401(t *testing.T) {
	svc := &fakeIntakeProcessor{}
	h := buildInternalHandler(svc, testInternalToken)
	rr := doIntake(h, payloadWithMessages("ph-001", "msg-001", "5511999990001"), "wrong-token")
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rr.Code)
	}
	if svc.calls != 0 {
		t.Errorf("service should not be called on auth failure, got %d calls", svc.calls)
	}
}

func TestInternalHandler_MissingToken_Returns401(t *testing.T) {
	svc := &fakeIntakeProcessor{}
	h := buildInternalHandler(svc, testInternalToken)
	rr := doIntake(h, payloadWithMessages("ph-001", "msg-001", "5511999990001"), "")
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rr.Code)
	}
}

func TestInternalHandler_TokenDisabled_NoCheck(t *testing.T) {
	svc := &fakeIntakeProcessor{}
	// Empty token = secondary check disabled (Cloud Run IAM is primary).
	h := buildInternalHandler(svc, "")
	rr := doIntake(h, payloadWithMessages("ph-001", "msg-001", "5511999990001"), "")
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rr.Code)
	}
}

func TestInternalHandler_CorrectToken_Passes(t *testing.T) {
	svc := &fakeIntakeProcessor{}
	h := buildInternalHandler(svc, testInternalToken)
	rr := doIntake(h, payloadWithMessages("ph-001", "msg-001", "5511999990001"), testInternalToken)
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rr.Code)
	}
}

// ─── No messages tests ────────────────────────────────────────────────────────

func TestInternalHandler_NoMessages_Returns200Ignored_NoWrite(t *testing.T) {
	svc := &fakeIntakeProcessor{}
	h := buildInternalHandler(svc, "")
	rr := doIntake(h, payloadWithNoMessages(), "")
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "ignored") {
		t.Errorf("body = %q, want 'ignored' status", rr.Body.String())
	}
	if svc.calls != 0 {
		t.Errorf("service should not be called when there are no messages, got %d calls", svc.calls)
	}
}

// ─── Service error tests ──────────────────────────────────────────────────────

func TestInternalHandler_ServiceError_Returns503(t *testing.T) {
	svc := &fakeIntakeProcessor{err: errors.New("mongodb connection error")}
	h := buildInternalHandler(svc, "")
	rr := doIntake(h, payloadWithMessages("ph-001", "msg-001", "5511999990001"), "")
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503", rr.Code)
	}
}

func TestInternalHandler_InvalidPayload_Returns400(t *testing.T) {
	svc := &fakeIntakeProcessor{err: ErrInvalidPayload}
	h := buildInternalHandler(svc, "")
	rr := doIntake(h, payloadWithMessages("ph-001", "msg-001", "5511999990001"), "")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400 when service returns ErrInvalidPayload", rr.Code)
	}
}

// ─── Valid intake tests ───────────────────────────────────────────────────────

func TestInternalHandler_ValidPayload_Returns200(t *testing.T) {
	svc := &fakeIntakeProcessor{}
	h := buildInternalHandler(svc, "")
	rr := doIntake(h, payloadWithMessages("ph-001", "msg-001", "5511999990001"), "")
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rr.Code)
	}
	if svc.calls != 1 {
		t.Errorf("service calls = %d, want 1", svc.calls)
	}
}

func TestInternalHandler_ValidPayload_PropagatesRequestID(t *testing.T) {
	svc := &fakeIntakeProcessor{}
	h := buildInternalHandler(svc, "")

	body := payloadWithMessages("ph-001", "msg-001", "5511999990001")
	req := httptest.NewRequest(http.MethodPost, "/internal/whatsapp/meta/messages/intake", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-EDN-Relay-Request-ID", "test-req-id-abc")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rr.Code)
	}
	if svc.input == nil {
		t.Fatal("expected service input to be set")
	}
	if svc.input.IngressRequestID != "test-req-id-abc" {
		t.Errorf("IngressRequestID = %q, want %q", svc.input.IngressRequestID, "test-req-id-abc")
	}
}

func TestInternalHandler_ValidPayload_PropagatesReceivedAt(t *testing.T) {
	svc := &fakeIntakeProcessor{}
	h := buildInternalHandler(svc, "")

	body := payloadWithMessages("ph-001", "msg-001", "5511999990001")
	req := httptest.NewRequest(http.MethodPost, "/internal/whatsapp/meta/messages/intake", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-EDN-Relay-Received-At", "2026-05-13T10:00:00.000000000Z")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rr.Code)
	}
	if svc.input == nil {
		t.Fatal("expected service input to be set")
	}
	want := time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)
	if !svc.input.ReceivedAt.Equal(want) {
		t.Errorf("ReceivedAt = %v, want %v", svc.input.ReceivedAt, want)
	}
}

func TestInternalHandler_BadJSON_Returns400(t *testing.T) {
	svc := &fakeIntakeProcessor{}
	h := buildInternalHandler(svc, "")
	req := httptest.NewRequest(http.MethodPost, "/internal/whatsapp/meta/messages/intake", bytes.NewReader([]byte("not-json")))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rr.Code)
	}
}
