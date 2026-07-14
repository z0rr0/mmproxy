package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type mockBotAPI struct {
	calls  int
	lastCh any
	lastTx string
	lastRe *models.ReplyParameters
}

func (m *mockBotAPI) SendMessage(_ context.Context, p *bot.SendMessageParams) (*models.Message, error) {
	m.calls++
	m.lastCh = p.ChatID
	m.lastTx = p.Text
	m.lastRe = p.ReplyParameters
	return &models.Message{}, nil
}

type mockPoster struct {
	calls   int
	channel string
	message string
	err     error
}

func (m *mockPoster) Post(_ context.Context, channelID, message string) error {
	m.calls++
	m.channel = channelID
	m.message = message
	return m.err
}

func newHandler(poster Poster, allowed ...int64) *Handler {
	set := make(map[int64]struct{}, len(allowed))
	for _, id := range allowed {
		set[id] = struct{}{}
	}
	return NewHandler(poster, "tg-channel", set)
}

// forwardedMsg builds an update from an allowlisted user with a forwarded text.
func forwardedMsg(userID int64, text string) *models.Update {
	return &models.Update{
		Message: &models.Message{
			ID:   555,
			From: &models.User{ID: userID},
			Chat: models.Chat{ID: 999},
			Text: text,
			ForwardOrigin: &models.MessageOrigin{
				Type:              models.MessageOriginTypeUser,
				MessageOriginUser: &models.MessageOriginUser{SenderUser: models.User{FirstName: "Src"}},
			},
		},
	}
}

func TestHandleEmptyUpdate(t *testing.T) {
	poster := &mockPoster{}
	api := &mockBotAPI{}
	h := newHandler(poster, 1)

	h.Handle(context.Background(), api, &models.Update{})                           // nil Message
	h.Handle(context.Background(), api, &models.Update{Message: &models.Message{}}) // nil From

	if poster.calls != 0 || api.calls != 0 {
		t.Errorf("expected no activity, got poster=%d api=%d", poster.calls, api.calls)
	}
}

func TestHandleUnauthorized(t *testing.T) {
	poster := &mockPoster{}
	api := &mockBotAPI{}
	h := newHandler(poster, 1) // user 2 not allowed

	h.Handle(context.Background(), api, forwardedMsg(2, "hi"))

	if poster.calls != 0 {
		t.Errorf("poster called %d times, want 0", poster.calls)
	}
	if api.calls != 0 {
		t.Errorf("should stay silent to strangers, but replied %d times", api.calls)
	}
}

func TestHandleNonForwarded(t *testing.T) {
	poster := &mockPoster{}
	api := &mockBotAPI{}
	h := newHandler(poster, 1)

	update := &models.Update{Message: &models.Message{
		ID: 7, From: &models.User{ID: 1}, Chat: models.Chat{ID: 999}, Text: "just chatting",
	}}
	h.Handle(context.Background(), api, update)

	if poster.calls != 0 {
		t.Errorf("poster called %d times, want 0", poster.calls)
	}
	if api.calls != 1 || !strings.Contains(api.lastTx, "Перешлите") {
		t.Errorf("expected forward hint reply, got calls=%d text=%q", api.calls, api.lastTx)
	}
}

func TestHandleForwardedText(t *testing.T) {
	poster := &mockPoster{}
	api := &mockBotAPI{}
	h := newHandler(poster, 1)

	h.Handle(context.Background(), api, forwardedMsg(1, "important news"))

	if poster.calls != 1 {
		t.Fatalf("poster called %d times, want 1", poster.calls)
	}
	if poster.channel != "tg-channel" {
		t.Errorf("channel = %q, want tg-channel", poster.channel)
	}
	if !strings.Contains(poster.message, "Forwarded from Src") || !strings.Contains(poster.message, "important news") {
		t.Errorf("message = %q, missing attribution or body", poster.message)
	}
	// Reply is a confirmation directed at the origin chat and message.
	if api.calls != 1 || !strings.Contains(api.lastTx, "Опубликовано") {
		t.Errorf("expected published confirmation, got calls=%d text=%q", api.calls, api.lastTx)
	}
	if api.lastCh != int64(999) {
		t.Errorf("reply chat = %v, want 999 (Chat.ID, not From.ID)", api.lastCh)
	}
	if api.lastRe == nil || api.lastRe.MessageID != 555 {
		t.Errorf("reply must reference message 555, got %+v", api.lastRe)
	}
}

func TestHandleCaptionUsedWhenTextEmpty(t *testing.T) {
	poster := &mockPoster{}
	api := &mockBotAPI{}
	h := newHandler(poster, 1)

	update := forwardedMsg(1, "")
	update.Message.Caption = "photo caption"
	h.Handle(context.Background(), api, update)

	if poster.calls != 1 {
		t.Fatalf("poster called %d times, want 1", poster.calls)
	}
	if !strings.Contains(poster.message, "photo caption") {
		t.Errorf("message = %q, want caption used", poster.message)
	}
}

func TestHandleForwardedNoText(t *testing.T) {
	poster := &mockPoster{}
	api := &mockBotAPI{}
	h := newHandler(poster, 1)

	update := forwardedMsg(1, "") // no text, no caption
	h.Handle(context.Background(), api, update)

	if poster.calls != 0 {
		t.Errorf("poster called %d times, want 0", poster.calls)
	}
	if api.calls != 1 || !strings.Contains(api.lastTx, "нет текста") {
		t.Errorf("expected no-text reply, got calls=%d text=%q", api.calls, api.lastTx)
	}
}

func TestHandlePosterError(t *testing.T) {
	poster := &mockPoster{err: errors.New("mattermost down")}
	api := &mockBotAPI{}
	h := newHandler(poster, 1)

	h.Handle(context.Background(), api, forwardedMsg(1, "news"))

	if api.calls != 1 {
		t.Fatalf("expected one error reply, got %d", api.calls)
	}
	if !strings.Contains(api.lastTx, "mattermost down") {
		t.Errorf("reply = %q, want it to include the error text", api.lastTx)
	}
}
