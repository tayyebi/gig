package components

import "html/template"

// OrderRow is one line in a buyer's or seller's order list.
type OrderRow struct {
	ID               int64
	GigSlug          string
	GigTitle         string
	CounterpartyName string
	StatusLabel      string
	Total            string
	CreatedAt        string
}

// OrdersListData backs both the buyer and seller order list pages.
type OrdersListData struct {
	Heading    string
	AsSeller   bool
	Orders     []OrderRow
	Pagination template.HTML
}

// OrdersListPage renders an order list.
func OrdersListPage(d OrdersListData) (template.HTML, error) {
	return execute("orderlist", d)
}

// TimelineEntry is one milestone shown on an order's status timeline.
type TimelineEntry struct {
	Label string
	At    string
}

// OrderMessageView is one displayable order-thread message.
type OrderMessageView struct {
	SenderName    string
	Body          string
	CreatedAt     string
	Mine          bool
	IsInfoRequest bool
}

// AttachmentView is a displayable, privately-served order file.
type AttachmentView struct {
	ID       int64
	FileName string
	Kind     string
}

// DeliveryView is a displayable delivery version. Files are not linked to a
// specific version (the schema has no such foreign key), so every delivery
// file for the order is shown once, alongside the deliveries, rather than
// attributed to one version.
type DeliveryView struct {
	Version   int
	Message   string
	CreatedAt string
}

// CancellationView is a displayable cancellation request.
type CancellationView struct {
	RequestedByMe bool
	Reason        string
	Status        string
}

// DisputeView is a displayable dispute.
type DisputeView struct {
	OpenedByMe bool
	Reason     string
	Status     string
	Decision   string
}

// ReviewView is a displayable review left on a completed order.
type ReviewView struct {
	Rating int
	Body   string
}

// OrderDetailData backs the order workspace page shown to the buyer, the
// seller, or an admin. Action visibility is precomputed in the handler
// (CanDeliver, CanAccept, ...) rather than re-deriving the state machine in
// the template.
type OrderDetailData struct {
	ID           int64
	GigSlug      string
	GigTitle     string
	StatusLabel  string
	BuyerName    string
	SellerName   string
	IsBuyer      bool
	IsSeller     bool
	IsAdmin      bool
	Requirements string
	Items        []CheckoutLineItem
	Subtotal     string
	PlatformFee  string
	Total        string
	Timeline     []TimelineEntry

	RevisionsUsed      int
	RevisionsAllowed   int
	Messages           []OrderMessageView
	Deliveries         []DeliveryView
	DeliveryAttachment []AttachmentView
	Cancellation       *CancellationView
	Dispute            *DisputeView
	DisputeAttachment  []AttachmentView
	Review             *ReviewView

	CanMessage        bool
	CanDeliver        bool
	CanRevise         bool
	CanAccept         bool
	CanCancelRequest  bool
	CanCancelResolve  bool
	CanDispute        bool
	CanResolveDispute bool
	CanReview         bool

	CSRF string
}

// OrderDetailPage renders the order workspace.
func OrderDetailPage(d OrderDetailData) (template.HTML, error) {
	return execute("orderdetail", d)
}
