package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Attachment kinds. Deliveries and dispute evidence are uploaded by whichever
// party is acting; "message" attachments ride along with a chat message.
const (
	AttachmentDelivery = "delivery"
	AttachmentMessage  = "message"
	AttachmentDispute  = "dispute_evidence"
)

// OrderMessage is one entry in an order's buyer/seller conversation.
type OrderMessage struct {
	ID            int64
	OrderID       int64
	SenderID      int64
	SenderName    string
	Body          string
	IsInfoRequest bool // seller explicitly asked the buyer for something, not just a general chat message
	CreatedAt     time.Time
}

// CreateOrderMessage appends a message to an order's thread.
func (s *Store) CreateOrderMessage(ctx context.Context, orderID, senderID int64, body string, isInfoRequest bool) (*OrderMessage, error) {
	m := OrderMessage{OrderID: orderID, SenderID: senderID, Body: body, IsInfoRequest: isInfoRequest}
	if err := s.db.QueryRowContext(ctx, `
		INSERT INTO order_messages (order_id, sender_id, body, is_info_request) VALUES ($1, $2, $3, $4)
		RETURNING id, created_at`,
		orderID, senderID, body, isInfoRequest,
	).Scan(&m.ID, &m.CreatedAt); err != nil {
		return nil, fmt.Errorf("create order message: %w", err)
	}
	return &m, nil
}

// ListOrderMessages returns an order's full conversation, oldest first.
func (s *Store) ListOrderMessages(ctx context.Context, orderID int64) ([]OrderMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT om.id, om.order_id, om.sender_id, u.name, om.body, om.is_info_request, om.created_at
		FROM order_messages om
		JOIN users u ON u.id = om.sender_id
		WHERE om.order_id = $1
		ORDER BY om.created_at, om.id`, orderID)
	if err != nil {
		return nil, fmt.Errorf("list order messages for order %d: %w", orderID, err)
	}
	defer rows.Close()

	var out []OrderMessage
	for rows.Next() {
		var m OrderMessage
		if err := rows.Scan(&m.ID, &m.OrderID, &m.SenderID, &m.SenderName, &m.Body, &m.IsInfoRequest, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan order message: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// OrderAttachment is a file uploaded against an order: a delivery, a message
// attachment, or dispute evidence. It is never served from the public
// /media/ path; the serving handler checks the caller is the order's buyer,
// seller, or an admin before streaming the file.
type OrderAttachment struct {
	ID         int64
	OrderID    int64
	UploaderID int64
	Kind       string
	FilePath   string
	FileName   string
	CreatedAt  time.Time
}

// CreateOrderAttachment records an uploaded file against an order.
func (s *Store) CreateOrderAttachment(ctx context.Context, orderID, uploaderID int64, kind, filePath, fileName string) (*OrderAttachment, error) {
	a := OrderAttachment{OrderID: orderID, UploaderID: uploaderID, Kind: kind, FilePath: filePath, FileName: fileName}
	if err := s.db.QueryRowContext(ctx, `
		INSERT INTO order_attachments (order_id, uploader_id, kind, file_path, file_name)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`,
		orderID, uploaderID, kind, filePath, fileName,
	).Scan(&a.ID, &a.CreatedAt); err != nil {
		return nil, fmt.Errorf("create order attachment: %w", err)
	}
	return &a, nil
}

// ListOrderAttachments returns an order's attachments, optionally filtered
// to one kind (pass "" for every kind), oldest first.
func (s *Store) ListOrderAttachments(ctx context.Context, orderID int64, kind string) ([]OrderAttachment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, order_id, uploader_id, kind, file_path, file_name, created_at
		FROM order_attachments
		WHERE order_id = $1 AND ($2 = '' OR kind = $2)
		ORDER BY created_at, id`, orderID, kind)
	if err != nil {
		return nil, fmt.Errorf("list order attachments for order %d: %w", orderID, err)
	}
	defer rows.Close()

	var out []OrderAttachment
	for rows.Next() {
		var a OrderAttachment
		if err := rows.Scan(&a.ID, &a.OrderID, &a.UploaderID, &a.Kind, &a.FilePath, &a.FileName, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan order attachment: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetOrderAttachment looks up a single attachment by ID, for the private
// serving handler to check access against before streaming the file.
func (s *Store) GetOrderAttachment(ctx context.Context, id int64) (*OrderAttachment, error) {
	var a OrderAttachment
	err := s.db.QueryRowContext(ctx, `
		SELECT id, order_id, uploader_id, kind, file_path, file_name, created_at
		FROM order_attachments WHERE id = $1`, id,
	).Scan(&a.ID, &a.OrderID, &a.UploaderID, &a.Kind, &a.FilePath, &a.FileName, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get order attachment %d: %w", id, err)
	}
	return &a, nil
}
