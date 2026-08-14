package main

import (
	"context"
	"fmt"

	"github.com/tayyebi/gig/services"
	"github.com/tayyebi/gig/store"
)

// notificationEmailKind must match handlers.notificationEmailJobKind. The two
// packages cannot share a constant without an import cycle (handlers depends
// on store and services, not on main), so the job kind string is duplicated
// at both enqueue sites instead.
const notificationEmailKind = "notification.email"

// notificationEmailPayload is the JSON shape handlers.Server.notify enqueues.
type notificationEmailPayload struct {
	UserID int64  `json:"user_id"`
	Body   string `json:"body"`
	Link   string `json:"link"`
}

// registerNotifications wires the handler that turns an in-app notification
// into a best-effort email.
func registerNotifications(q *jobQueue) {
	q.Register(notificationEmailKind, sendNotificationEmail)
}

func sendNotificationEmail(ctx context.Context, jc *jobContext, job store.Job) error {
	var p notificationEmailPayload
	if err := job.DecodePayload(&p); err != nil {
		return fmt.Errorf("decode notification email payload: %w", err)
	}
	u, err := jc.Store.GetUserByID(ctx, p.UserID)
	if err != nil {
		return fmt.Errorf("load user %d: %w", p.UserID, err)
	}
	body := p.Body
	if p.Link != "" {
		body += "\n\n" + jc.Cfg.BaseURL + p.Link
	}
	if err := jc.Mailer.Send(ctx, services.Email{To: u.Email, Subject: "Gig Marketplace notification", Body: body}); err != nil {
		return fmt.Errorf("send notification email: %w", err)
	}
	return nil
}
