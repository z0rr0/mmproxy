// Package telegram implements the Telegram bot source: it accepts forwarded
// messages from allowlisted users and republishes their text into Mattermost.
package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const (
	msgForwardHint = "Перешлите (forward) сообщение — я опубликую его текст в Mattermost."
	msgNoText      = "В пересланном сообщении нет текста, медиа не переносятся."
	msgPublished   = "Опубликовано в Mattermost."
	msgPostFailed  = "Не удалось опубликовать в Mattermost: "
)

// BotAPI is the subset of *bot.Bot the handler needs. Defined on the consumer
// side so tests can supply a mock.
type BotAPI interface {
	SendMessage(ctx context.Context, params *bot.SendMessageParams) (*models.Message, error)
}

// Poster publishes a message to a Mattermost channel.
type Poster interface {
	Post(ctx context.Context, channelID, message string) error
}

// Handler holds the dependencies for processing Telegram updates.
type Handler struct {
	poster    Poster
	channelID string
	allowed   map[int64]struct{}
}

// Bot wraps the Telegram client with lifecycle tracking for handler goroutines.
// Start stops accepting updates when its context is cancelled; Shutdown waits
// for handlers that were already dispatched and cancels them on timeout.
type Bot struct {
	api            *bot.Bot
	handler        *Handler
	handlerCtx     context.Context //nolint:containedctx // deliberately outlives the polling context
	cancelHandlers context.CancelFunc
	handlers       sync.WaitGroup
}

// NewHandler builds a Handler that posts to channelID for users in allowed.
func NewHandler(poster Poster, channelID string, allowed map[int64]struct{}) *Handler {
	return &Handler{poster: poster, channelID: channelID, allowed: allowed}
}

// NewBot constructs a bot with the handler wired as the default handler for all
// updates.
func NewBot(token string, h *Handler) (*Bot, error) {
	handlerCtx, cancelHandlers := context.WithCancel(context.Background())
	b := &Bot{
		handler:        h,
		handlerCtx:     handlerCtx,
		cancelHandlers: cancelHandlers,
	}

	api, err := bot.New(
		token,
		bot.WithNotAsyncHandlers(),
		bot.WithDefaultHandler(b.dispatch),
	)
	if err != nil {
		cancelHandlers()
		return nil, err
	}
	b.api = api
	return b, nil
}

// Start polls Telegram until ctx is cancelled. Handler completion is managed
// separately by Shutdown so an in-flight post is not cancelled with polling.
func (b *Bot) Start(ctx context.Context) {
	b.api.Start(ctx)
}

// Shutdown waits for every handler registered before Start returned. Callers
// must stop and join Start first, ensuring no concurrent WaitGroup additions.
func (b *Bot) Shutdown(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		b.handlers.Wait()
		close(done)
	}()

	select {
	case <-done:
		b.cancelHandlers()
		return nil
	case <-ctx.Done():
		b.cancelHandlers()
		return fmt.Errorf("wait for telegram handlers: %w", ctx.Err())
	}
}

// Handle is the testable core: it validates the update, enforces the allowlist,
// requires a forwarded message with text, and republishes it to Mattermost.
func (h *Handler) Handle(ctx context.Context, api BotAPI, update *models.Update) {
	if emptyUpdate(update) {
		return
	}
	msg := update.Message

	if _, ok := h.allowed[msg.From.ID]; !ok {
		// Do not reply to strangers: a silent drop avoids turning the bot into
		// a spam amplifier.
		slog.Warn("telegram: unauthorized user",
			"user_id", msg.From.ID, "username", msg.From.Username)
		return
	}

	if msg.ForwardOrigin == nil {
		h.reply(ctx, api, msg, msgForwardHint)
		return
	}

	text := msg.Text
	if text == "" {
		text = msg.Caption
	}
	if text == "" {
		h.reply(ctx, api, msg, msgNoText)
		return
	}

	formatted := FormatForwarded(msg.ForwardOrigin, text)
	if err := h.poster.Post(ctx, h.channelID, formatted); err != nil {
		slog.Error("telegram: post failed", "error", err, "user_id", msg.From.ID)
		h.reply(ctx, api, msg, msgPostFailed+err.Error())
		return
	}
	h.reply(ctx, api, msg, msgPublished)
}

// reply sends text back to the originating chat as a reply to the message.
func (h *Handler) reply(ctx context.Context, api BotAPI, msg *models.Message, text string) {
	_, err := api.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            text,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
	if err != nil {
		slog.Error("telegram: send reply failed", "error", err, "chat_id", msg.Chat.ID)
	}
}

// dispatch is called synchronously by the SDK worker. It registers the handler
// before starting its goroutine, so Start cannot return ahead of registration.
func (b *Bot) dispatch(ctx context.Context, api *bot.Bot, update *models.Update) {
	b.dispatchHandler(ctx, api, update)
}

func (b *Bot) dispatchHandler(pollingCtx context.Context, api BotAPI, update *models.Update) {
	if pollingCtx.Err() != nil {
		return
	}
	b.startHandler(b.handlerCtx, api, update) //nolint:contextcheck // handler work must outlive polling
}

func (b *Bot) startHandler(ctx context.Context, api BotAPI, update *models.Update) {
	b.handlers.Go(func() {
		b.handler.Handle(ctx, api, update)
	})
}

// emptyUpdate reports whether the update lacks a usable message or sender.
func emptyUpdate(update *models.Update) bool {
	return update == nil || update.Message == nil || update.Message.From == nil
}
